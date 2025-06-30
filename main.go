package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/skip2/go-qrcode"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"teProj/cryptoManager"
	"time"
)

var isUnderMaintenance = false

type RateDisplay struct {
	From string
	To   string
	ToId int
	Rate float64
}

type ConversionResult struct {
	FromAsset      string
	ToAsset        string
	Rate           string
	Fee            float64
	AmountAfterFee string
	RatePerUnit    string
}

func formatCryptoValue(amount float64, currency int) string {
	var digitCount int64 = 8
	for _, crypto := range config.SupportedCryptos {
		if crypto.InternalAssetID == int(currency) {
			digitCount = int64(crypto.Precision)
			break
		}
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%."+strconv.FormatInt(digitCount, 10)+"f", amount), "0"), ".")
}

func GenerateQRCodeDataURL(input string) template.URL {
	var err error

	qr, err := qrcode.New(input, qrcode.Medium)
	if err != nil {
		return ""
	}

	qr.DisableBorder = true

	var png []byte
	png, err = qr.PNG(256)
	if err != nil {
		return ""
	}

	var b bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &b)
	_, err = encoder.Write(png)
	if err != nil {
		return ""
	}
	encoder.Close()

	dataURL := "data:image/png;base64," + b.String()
	url := template.URL(dataURL)
	return url
}

func mainPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	action := r.URL.Query().Get("action")
	fromID, _ := strconv.Atoi(r.URL.Query().Get("fromId"))
	toID, _ := strconv.Atoi(r.URL.Query().Get("toId"))
	amount, _ := strconv.ParseFloat(r.URL.Query().Get("amount"), 64)
	address := r.URL.Query().Get("address")
	refundAddress := r.URL.Query().Get("addressRefund")
	var amountString string
	if action == "calc" || action == "exec" {
		amountString = strconv.FormatFloat(amount, 'f', -1, 64)
	} else {
		amountString = ""
	}

	type ReserveDisplay struct {
		Name    string
		Balance string
		Icon    string
	}

	var reserves []ReserveDisplay

	for internalID, handler := range handlers {
		var crypto *CryptoCurrency
		for _, c := range config.SupportedCryptos {
			if c.InternalAssetID == int(internalID) {
				crypto = &c
				break
			}
		}
		if crypto == nil {
			continue
		}
		balance, err := handler.CheckBalance()
		if err != nil {
			log.Printf("Error getting balance for %s: %v", crypto.AssetName, err)
			balance = 0
		}
		iconPath := fmt.Sprintf("/asset_cache/%d.png", crypto.InternalAssetID)
		reserves = append(reserves, ReserveDisplay{
			Name:    crypto.AssetName,
			Balance: formatCryptoValue(balance, int(internalID)),
			Icon:    iconPath,
		})
	}

	data := struct {
		Cryptos           []CryptoCurrency
		Rates             []RateDisplay
		Conversion        *ConversionResult
		Error             string
		FormFrom          int
		FormTo            int
		FormAmount        float64
		FormAmountString  string
		FormAddress       string
		FormRefundAddress string
		Action            string
		SelectedCrypto    *CryptoCurrency
		Reserves          []ReserveDisplay
	}{
		Cryptos:           config.SupportedCryptos,
		FormFrom:          fromID,
		FormTo:            toID,
		FormAmount:        amount,
		FormAmountString:  amountString,
		FormAddress:       address,
		FormRefundAddress: refundAddress,
		Action:            action,
		Reserves:          reserves,
	}

	for _, c := range config.SupportedCryptos {
		if c.InternalAssetID == toID {
			data.SelectedCrypto = &c
			break
		}
	}

	for _, fee := range config.Routes {
		rate, err := ConvertWithoutFee(store, fee.Pair.IDFrom, fee.Pair.IDTo, 1)
		if err == nil {
			data.Rates = append(data.Rates, RateDisplay{
				From: store.assetNames[fee.Pair.IDFrom],
				To:   store.assetNames[fee.Pair.IDTo],
				ToId: fee.Pair.IDTo,
				Rate: rate,
			})
		}
	}
	if action != "" && fromID > 0 && toID > 0 {
		if action == "calc" && amount > 0 {
			if _, ok := store.conversionFees[fmt.Sprintf("%d-%d", fromID, toID)]; !ok {
				data.Error = "Route unavailable"
			} else {
				rate, err := Convert(store, fromID, toID, amount)
				if err == nil {
					fee, _ := store.GetFee(fromID, toID)
					data.Conversion = &ConversionResult{
						FromAsset:      store.assetNames[fromID],
						ToAsset:        store.assetNames[toID],
						Rate:           formatCryptoValue(rate/(1-fee), toID),
						Fee:            fee,
						AmountAfterFee: formatCryptoValue(rate, toID),
						RatePerUnit:    formatCryptoValue((rate/(1-fee))/amount, toID),
					}
				} else {
					data.Error = "Conversion failed"
				}
			}
		} else if action == "exec" {
			if isUnderMaintenance {
				data.Error = "Service is under maintenance"
			} else {
				rate, err := Convert(store, fromID, toID, amount)
				if err == nil {
					orderID, err := MakeSession(fromID, toID, amount, rate, address, refundAddress)
					if err != nil {
						data.Error = fmt.Sprintf("Exchange failed: %v", err)
					} else {
						go func() {
							sessionsMutex.Lock()
							orderSession := Sessions[orderID]
							sessionsMutex.Unlock()
							err := ExchangeBackend(orderSession)
							if err != nil {
								sessionsMutex.Lock()
								session := Sessions[orderID]
								sessionsMutex.Unlock()
								//Careful here
								if session.FromTransaction.Txid != "nil" {
									send, err := session.FromCurrency.Send(cryptoManager.CryptoAddress{
										Address:   session.RefundAddress,
										StartTime: 0,
									}, session.FromTransaction.Amount)
									if err != nil {
										LogError("Refund failed with error: %s, %#v", err.Error(), *session)
									} else {
										LogActivity("Refund succeeded [%v], %#v", send, *session)
									}
								}
							}
						}()
						http.Redirect(w, r, "/order?orderID="+orderID, http.StatusSeeOther)
						return
					}
				} else {
					data.Error = "Conversion failed"
				}
			}

		}
	}
	tmpl := template.Must(template.New("index.html").Funcs(template.FuncMap{
		"multiply":     func(a, b float64) float64 { return a * b },
		"formatCrypto": formatCryptoValue,
	}).ParseFiles("templates/index.html"))
	tmpl.Execute(w, data)
}

var orderFunctions = template.FuncMap{
	"multiply":              func(a, b float64) float64 { return a * b },
	"formatCrypto":          formatCryptoValue,
	"generateQrCode":        GenerateQRCodeDataURL,
	"formatExpirationTimer": FormatExpirationTime,
	"calc":                  func(a int64, b int) float64 { return (float64(a) / float64(b)) * 100 },
	"add":                   func(a, b int) int { return a + b },
}

var orderTemplates = map[string]string{
	"CREATED":            "created.html",
	"AWAITING INPUT":     "awaiting_input.html",
	"CONFIRMING INPUT":   "confirming_input.html",
	"EXCHANGING":         "exchanging.html",
	"CONFIRMING OUTPUT":  "confirming_output.html",
	"SUCCESS":            "success.html",
	"TRANSLATION FAILED": "transaction_failed.html",
}

func orderPage(w http.ResponseWriter, r *http.Request) {
	orderID := r.URL.Query().Get("orderID")

	sessionsMutex.Lock()
	session, ok := Sessions[orderID]
	sessionsMutex.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	templateFile, found := orderTemplates[session.Status]
	if !found {
		http.Error(w, "Invalid session state", http.StatusInternalServerError)
		return
	}

	tmpl, err := template.New(templateFile).Funcs(orderFunctions).ParseFiles("templates/" + templateFile)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		fmt.Println("Template parsing error:", err)
		return
	}

	err = tmpl.Execute(w, session)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		fmt.Println("Template execution error:", err)
	}

}

func run() {
	var err error
	err = InitLogging("./logs")
	if err != nil {
		log.Fatal("Failed to initialize logger:", err)
		return
	}
	config, err = loadConfig("SupportedCryptos.json")
	if err != nil {
		log.Fatal("Failed to load config:", err)
		return
	}

	if err = cacheAssets(config); err != nil {
		log.Fatal("Failed to cache assets:", err)
	}

	store = NewPriceStore(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ConnectWebSocket(ctx)

	mux := http.NewServeMux()

	fs := http.FileServer(http.Dir("./asset_cache"))
	fss := http.FileServer(http.Dir("./styles"))

	mux.Handle("/asset_cache/", http.StripPrefix("/asset_cache/", fs))
	mux.Handle("/styles/", http.StripPrefix("/styles/", fss))

	mux.HandleFunc("/", mainPage)
	mux.HandleFunc("/order", orderPage)
	//mux.HandleFunc("/test", testPage)
	fmt.Println("Server started at port 80")
	go http.ListenAndServe(":80", mux)
}

func waitForAllOrdersToComplete() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		allComplete := true

		sessionsMutex.Lock()
		for _, session := range Sessions {
			switch session.Status {
			case "TRANSLATION FAILED", "SUCCESS":
				// Terminal state, no action needed
				continue
			default:
				allComplete = false
			}
		}
		sessionsMutex.Unlock()

		if allComplete {
			return
		}
	}

}

func main() {
	run()
	for {
		var command string
		fmt.Scanln(&command)
		switch command {
		case "maintain":
			isUnderMaintenance = true
			waitForAllOrdersToComplete()
			fmt.Println("All orders are done you may edit environment")
		case "resume":
			isUnderMaintenance = false
		default:
			continue
		}

	}
}
