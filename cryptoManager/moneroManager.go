package cryptoManager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/icholy/digest"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const xmrhost = "<XMRNODERPCHOST>"
const xmrWallethost = "<XMRWALLETRPCHOST>"
const xmruser = "<XMRRPCUSER>"
const xmrpass = "<XMRRPCPASS>"
const xmrwalletName = "<WALLETNAME>"

var XmrBlockchainExplorers = []*CryptoTransactionExplorer{
	{
		Name:     "localmonero",
		IconPath: "asset_cache/3.png",
		UrlResolver: func(s string) string {
			return "https://localmonero.co/blocks/tx/" + s
		},
	},
}

type XmrHandler struct {
	host          string
	xmrWalletHost string
	user          string
	pass          string
	wallet        string
	client        *http.Client
	sendMutex     sync.Mutex
}

func callGlobalXmrRPC(handler *XmrHandler, method string, params map[string]interface{}) (map[string]interface{}, error) {
	requestBody := map[string]interface{}{
		"id":     "xmr-handler",
		"method": method,
		"params": params,
	}
	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", "http://"+handler.host+"/json_rpc", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := handler.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if errResult := result["error"]; errResult != nil {
		errorMap := errResult.(map[string]interface{})
		code := errorMap["code"].(float64)
		message := errorMap["message"].(string)
		return nil, fmt.Errorf("RPC error %d: %s", int(code), message)
	}

	return result, nil
}

func callWalletXmrRPC(handler *XmrHandler, method string, params map[string]interface{}) (map[string]interface{}, error) {
	requestBody := map[string]interface{}{
		"id":     "xmr-handler",
		"method": method,
		"params": params,
	}
	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", "http://"+handler.xmrWalletHost+"/json_rpc", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := handler.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if errResult := result["error"]; errResult != nil {
		errorMap := errResult.(map[string]interface{})
		code := errorMap["code"].(float64)
		message := errorMap["message"].(string)
		return nil, fmt.Errorf("RPC error %d: %s", int(code), message)
	}

	return result, nil
}

func NewXmrHandler() (*XmrHandler, error) {
	tempClient := &http.Client{Transport: &digest.Transport{
		Username: xmruser,
		Password: xmrpass,
	},
		Timeout: 10 * time.Second}

	handler := &XmrHandler{
		host:          xmrhost,
		xmrWalletHost: xmrWallethost,
		user:          xmruser,
		pass:          xmrpass,
		wallet:        xmrwalletName,

		client: tempClient,
	}
	_, err := callGlobalXmrRPC(handler, "get_version", nil)
	if err != nil {
		return nil, err
	}

	_, err = callWalletXmrRPC(handler, "open_wallet", map[string]interface{}{
		"filename": handler.wallet,
	})
	if err == nil {
		return handler, nil
	} else if strings.Contains(err.Error(), "file not found") {
		_, err = callWalletXmrRPC(handler, "create_wallet", map[string]interface{}{
			"filename": handler.wallet,
			"language": "English",
		})
		if err != nil {
			return nil, err
		} else {
			return handler, nil
		}
	} else if strings.Contains(err.Error(), "is opened") {
		return handler, nil
	} else {
		return nil, err
	}
}

func (h *XmrHandler) CheckBalance() (float64, error) {
	result, err := callWalletXmrRPC(h, "get_balance", map[string]interface{}{
		"account_index": 0,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get balance: %v", err)
	}

	result, ok := result["result"].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("unexpected response format from get_balance")
	}

	atomicBalance, ok := result["unlocked_balance"].(float64)

	if !ok {
		return 0, fmt.Errorf("unexpected response format from get_balance")
	}

	balance := atomicBalance / 1e12

	return balance, nil
}

func (h *XmrHandler) GenerateNewAddress() (CryptoAddress, error) {
	result, err := callWalletXmrRPC(h, "create_address", map[string]interface{}{"account_index": 0})
	if err != nil {
		return CryptoAddress{}, err
	}

	result, ok := result["result"].(map[string]interface{})
	if !ok {
		return CryptoAddress{}, fmt.Errorf("unexpected response format from create_address")
	}

	address, ok := result["address"].(string)
	if !ok {
		return CryptoAddress{}, fmt.Errorf("unexpected response format from create_address")
	}
	return CryptoAddress{
		Address:   address,
		StartTime: time.Now().Unix(),
	}, nil
}

func (h *XmrHandler) GetAddressTransaction(address CryptoAddress) (*CryptoTransaction, error) {
	result, err := callWalletXmrRPC(h, "get_transfers", map[string]interface{}{
		"in":            true,
		"account_index": 0,
	})
	if err != nil {
		return nil, err
	}

	result, ok := result["result"].(map[string]interface{})

	if !ok {
		return nil, fmt.Errorf("unexpected response format from get_transfers")
	}

	inBalances, ok := result["in"].([]interface{})

	type txInfo struct {
		Txid          string
		Confirmations int64
		Amount        float64
		Time          int64
	}

	var relevantTransactions []txInfo

	for _, balance := range inBalances {
		txMap, ok := balance.(map[string]interface{})
		if !ok {
			continue
		}
		txAddress, _ := txMap["address"].(string)
		category, _ := txMap["type"].(string)
		if category != "in" || txAddress != address.Address {
			continue
		}
		var txTime int64
		if txTimeVal, ok := txMap["timestamp"].(float64); ok {
			txTime = int64(txTimeVal)
		} else {
			continue
		}
		if txTime < address.StartTime {
			continue
		}
		confirmations, _ := txMap["confirmations"].(float64)
		amount, _ := txMap["amount"].(float64)
		txid, _ := txMap["txid"].(string)
		relevantTransactions = append(relevantTransactions, txInfo{
			Txid:          txid,
			Confirmations: int64(confirmations),
			Amount:        amount / 1e12,
			Time:          txTime,
		})
	}

	if len(relevantTransactions) == 0 {
		return nil, nil
	}

	sort.Slice(relevantTransactions, func(i, j int) bool {
		return relevantTransactions[i].Time < relevantTransactions[j].Time
	})

	newest := relevantTransactions[0]

	return &CryptoTransaction{
		Txid:          newest.Txid,
		Confirmations: newest.Confirmations,
		Amount:        newest.Amount,
		Explorers:     XmrBlockchainExplorers,
	}, nil
}

func (h *XmrHandler) GetTransactionDetails(txid string) (*CryptoTransaction, error) {
	result, err := callWalletXmrRPC(h, "get_transfer_by_txid", map[string]interface{}{
		"txid":          txid,
		"account_index": 0,
	})
	if err != nil {
		return nil, err
	}
	result, ok := result["result"].(map[string]interface{})

	if !ok {
		return nil, fmt.Errorf("unexpected response format from get_transfer_by_txid")
	}

	transfer, ok := result["transfer"].(map[string]interface{})

	if !ok {
		return nil, fmt.Errorf("unexpected response format from get_transfer_by_txid")
	}

	confirmations, _ := transfer["confirmations"].(float64)
	amount, _ := transfer["amount"].(float64)

	return &CryptoTransaction{
		Txid:          txid,
		Confirmations: int64(confirmations),
		Amount:        amount / 1e12,
		Explorers:     XmrBlockchainExplorers,
	}, nil
}

func (h *XmrHandler) Send(address CryptoAddress, amount float64) ([]string, error) {
	h.sendMutex.Lock()
	defer h.sendMutex.Unlock()
	result, err := callWalletXmrRPC(h, "transfer", map[string]interface{}{
		"destinations": []interface{}{
			map[string]interface{}{
				"amount":  amount * 1e12,
				"address": address.Address,
			},
		},
		"account_index":             0,
		"subtract_fee_from_outputs": []interface{}{0},
		"priority":                  2,
		"ring_size":                 16,
		"unlock_time":               0,
	})
	if err != nil {
		return nil, err
	}
	result, ok := result["result"].(map[string]interface{})

	if !ok {
		return nil, fmt.Errorf("unexpected response format from transfer")
	}

	txid, ok := result["tx_hash"].(string)

	if !ok {
		return nil, fmt.Errorf("unexpected response format from transfer")
	}

	return []string{txid}, nil

}
