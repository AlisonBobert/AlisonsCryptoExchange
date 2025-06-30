package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PriceStore struct {
	sync.RWMutex
	prices         map[int]float64
	cmcToInternal  map[int]int
	internalToCmc  map[int]int
	assetNames     map[int]string
	conversionFees map[string]float64
	minAmounts     map[string]float64
}

var store *PriceStore

func NewPriceStore(config *Config) *PriceStore {
	store := &PriceStore{
		prices:         make(map[int]float64),
		cmcToInternal:  make(map[int]int),
		internalToCmc:  make(map[int]int),
		assetNames:     make(map[int]string),
		conversionFees: make(map[string]float64),
		minAmounts:     make(map[string]float64),
	}

	for _, crypto := range config.SupportedCryptos {
		store.cmcToInternal[crypto.CoinmarketcapAssetID] = crypto.InternalAssetID
		store.internalToCmc[crypto.InternalAssetID] = crypto.CoinmarketcapAssetID
		store.assetNames[crypto.InternalAssetID] = crypto.AssetName
	}

	for _, route := range config.Routes {
		key := fmt.Sprintf("%d-%d", route.Pair.IDFrom, route.Pair.IDTo)
		store.conversionFees[key] = route.Fee
		store.minAmounts[key] = route.MinAmount
	}

	return store
}

func (ps *PriceStore) Update(cmcID int, price float64) {
	ps.Lock()
	defer ps.Unlock()
	if internalID, exists := ps.cmcToInternal[cmcID]; exists {
		ps.prices[internalID] = price
	}
}

func (ps *PriceStore) Get(internalID int) (float64, bool) {
	ps.RLock()
	defer ps.RUnlock()
	price, ok := ps.prices[internalID]
	return price, ok
}

func (ps *PriceStore) GetFee(fromID, toID int) (float64, bool) {
	ps.RLock()
	defer ps.RUnlock()
	fee, ok := ps.conversionFees[fmt.Sprintf("%d-%d", fromID, toID)]
	return fee, ok
}

func ConnectWebSocket(ctx context.Context) {
	var conn *websocket.Conn
	var err error
	dialer := websocket.DefaultDialer

	headers := http.Header{
		"Origin":          []string{"https://coinmarketcap.com"},
		"User-Agent":      []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36 Edg/135.0.0.0"},
		"Accept-Language": []string{"en-GB,en;q=0.9,en-US;q=0.8,ht;q=0.7,ru;q=0.6"},
		"Cache-Control":   []string{"no-cache"},
		"Pragma":          []string{"no-cache"},
	}

	retryBackoff := time.Second
	const maxBackoff = 30 * time.Second

	var cmcIDs []string
	for _, crypto := range config.SupportedCryptos {
		cmcIDs = append(cmcIDs, strconv.Itoa(crypto.CoinmarketcapAssetID))
	}
	subscriptionIDs := strings.Join(cmcIDs, ",")

marker:
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, _, err = dialer.Dial(
				"wss://push.coinmarketcap.com/ws?device=web&client_source=coin_detail_page",
				headers,
			)

			if err != nil {
				//log.Printf("Connection error: %v. Retrying in %v", err, retryBackoff)
				time.Sleep(retryBackoff)
				retryBackoff = min(retryBackoff*2, maxBackoff)
				continue
			}

			retryBackoff = time.Second
			//log.Println("Successfully connected to WebSocket")

			subMsg := `{"method":"RSUBSCRIPTION","params":["main-site@crypto_price_15s@{}@normal","` + subscriptionIDs + `"]}`
			if err := conn.WriteMessage(websocket.TextMessage, []byte(subMsg)); err != nil {
				//log.Println("Failed to send subscription:", err)
				conn.Close()
				continue
			}

			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					//log.Println("Read error:", err)
					goto marker
				}

				var response struct {
					Data struct {
						ID int     `json:"id"`
						P  float64 `json:"p"`
					} `json:"d"`
				}

				if err := json.Unmarshal(message, &response); err != nil {
					//log.Println("JSON parse error:", err)
					continue
				}

				store.Update(response.Data.ID, response.Data.P)
				//log.Printf("Updated price for %d: $%.2f", response.Data.ID, response.Data.P)
			}

		}
	}
}

func Convert(store *PriceStore, fromID, toID int, amount float64) (float64, error) {
	fromPrice, ok := store.Get(fromID)
	if !ok {
		return 0, fmt.Errorf("price not available for %s", store.assetNames[fromID])
	}

	toPrice, ok := store.Get(toID)
	if !ok {
		return 0, fmt.Errorf("price not available for %s", store.assetNames[toID])
	}

	fee, ok := store.GetFee(fromID, toID)
	if !ok {
		return 0, fmt.Errorf("conversion fee not found for %s to %s",
			store.assetNames[fromID], store.assetNames[toID])
	}

	usdValue := amount * fromPrice
	usdValueAfterFee := usdValue * (1 - fee)
	return usdValueAfterFee / toPrice, nil
}

func ConvertWithoutFee(store *PriceStore, fromID, toID int, amount float64) (float64, error) {
	fromPrice, ok := store.Get(fromID)
	if !ok {
		return 0, fmt.Errorf("price not available for %s", store.assetNames[fromID])
	}

	toPrice, ok := store.Get(toID)
	if !ok {
		return 0, fmt.Errorf("price not available for %s", store.assetNames[toID])
	}
	usdValue := amount * fromPrice
	return usdValue / toPrice, nil
}
