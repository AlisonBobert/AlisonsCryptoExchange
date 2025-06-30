package cryptoManager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const btchost = "<BTCNODEHOST>"
const btcuser = "<BTCRPCUSER>"
const btcpass = "<BTCRPCPASS>"
const btcwalletName = "<WALLETNAME>"

var BtcBlockchainExplorers = []*CryptoTransactionExplorer{
	{
		Name:     "mempool",
		IconPath: "asset_cache/mempool.png",
		UrlResolver: func(s string) string {
			return "https://mempool.space/tx/" + s
		},
	},
	{
		Name:     "blockstream",
		IconPath: "asset_cache/blockstream.png",
		UrlResolver: func(s string) string {
			return "https://blockstream.info/tx/" + s
		},
	},
}

type BtcHandler struct {
	host      string
	user      string
	pass      string
	wallet    string
	client    *http.Client
	sendMutex sync.Mutex
}

func callGlobalBtcRPC(handler *BtcHandler, method string, params []interface{}) (map[string]interface{}, error) {
	requestBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "btc-handler",
		"method":  method,
		"params":  params,
	}
	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", "http://"+handler.host, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(handler.user, handler.pass)
	req.Header.Set("Content-Type", "text/plain")

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

func NewBtcHandler() (*BtcHandler, error) {
	tempClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	handler := &BtcHandler{
		host:   btchost,
		user:   btcuser,
		pass:   btcpass,
		wallet: btcwalletName,
		client: tempClient,
	}

	result, err := callGlobalBtcRPC(handler, "listwallets", nil)
	if err != nil {
		return nil, err
	}

	wallets, ok := result["result"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format from listwallets")
	}

	walletLoaded := false
	for _, w := range wallets {
		if w.(string) == handler.wallet {
			walletLoaded = true
			break
		}
	}

	if !walletLoaded {
		_, err := callGlobalBtcRPC(handler, "loadwallet", []interface{}{handler.wallet})
		if err != nil {
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "Wallet file verification failed") {
				createParams := []interface{}{
					handler.wallet, // wallet name
					false,          // disable_private_keys
					false,          // blank
					"",             // passphrase
					true,           // avoid_reuse
				}
				_, createErr := callGlobalBtcRPC(handler, "createwallet", createParams)
				if createErr != nil {
					return nil, fmt.Errorf("failed to create wallet: %v", createErr)
				}
			} else {
				return nil, err
			}
		}
	}

	return handler, nil
}

func (h *BtcHandler) rpcWalletCall(method string, params []interface{}) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s/wallet/%s", h.host, h.wallet)
	requestBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "btc-handler",
		"method":  method,
		"params":  params,
	}

	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("request creation failed: %v", err)
	}

	req.SetBasicAuth(h.user, h.pass)
	req.Header.Set("Content-Type", "text/plain")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RPC request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	if errResult := result["error"]; errResult != nil {
		errorMap := errResult.(map[string]interface{})
		code := errorMap["code"].(float64)
		message := errorMap["message"].(string)
		return nil, fmt.Errorf("RPC error %d: %s", int(code), message)
	}

	return result, nil
}

func (h *BtcHandler) CheckBalance() (float64, error) {
	result, err := h.rpcWalletCall("getbalance", []interface{}{"*", 1})
	if err != nil {
		return 0, fmt.Errorf("failed to get balance: %v", err)
	}

	balance, ok := result["result"].(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected response format from getbalance")
	}

	return balance, nil
}

func (h *BtcHandler) GenerateNewAddress() (CryptoAddress, error) {
	result, err := h.rpcWalletCall("getnewaddress", []interface{}{"", "bech32"})
	if err != nil {
		return CryptoAddress{}, err
	}

	address, ok := result["result"].(string)
	if !ok {
		return CryptoAddress{}, fmt.Errorf("unexpected response format from getnewaddress")
	}

	return CryptoAddress{
		Address:   address,
		StartTime: time.Now().Unix(),
	}, nil
}

func (h *BtcHandler) GetAddressTransaction(address CryptoAddress) (*CryptoTransaction, error) {
	result, err := h.rpcWalletCall("listtransactions", []interface{}{"*", 1000000, 0, true})
	if err != nil {
		return nil, err
	}

	rawTransactions, ok := result["result"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format from listtransactions")
	}

	type txInfo struct {
		Txid          string
		Confirmations int64
		Amount        float64
		Time          int64
	}

	var relevantTransactions []txInfo

	for _, txInterface := range rawTransactions {
		txMap, ok := txInterface.(map[string]interface{})
		if !ok {
			continue
		}

		category, _ := txMap["category"].(string)
		txAddress, _ := txMap["address"].(string)
		if category != "receive" || txAddress != address.Address {
			continue
		}

		var txTime int64
		if txTimeVal, ok := txMap["time"].(float64); ok {
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
			Amount:        amount,
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
		Explorers:     BtcBlockchainExplorers,
	}, nil
}

func (h *BtcHandler) GetTransactionDetails(txid string) (*CryptoTransaction, error) {
	result, err := h.rpcWalletCall("gettransaction", []interface{}{txid})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve transaction: %v", err)
	}

	txData, ok := result["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid transaction response format")
	}

	confirmations, _ := txData["confirmations"].(float64)
	amount, _ := txData["amount"].(float64)

	return &CryptoTransaction{
		Txid:          txid,
		Confirmations: int64(confirmations),
		Amount:        amount,
		Explorers:     BtcBlockchainExplorers,
	}, nil
}

func (h *BtcHandler) Send(address CryptoAddress, amount float64) ([]string, error) {
	h.sendMutex.Lock()
	defer h.sendMutex.Unlock()
	balance, err := h.CheckBalance()
	if err != nil {
		return nil, err
	}

	if balance < amount {
		return nil, fmt.Errorf("insufficient funds: available %.8f BTC, required %.8f BTC", balance, amount)
	}

	result, err := h.rpcWalletCall("sendtoaddress", []interface{}{
		address.Address,
		RoundToNDigits(amount, 8),
		"",
		"",
		true,
		false,
		1,
		"CONSERVATIVE",
	})
	if err != nil {
		return nil, err
	}

	txid, ok := result["result"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid transaction response format")
	}

	return []string{txid}, nil
}
