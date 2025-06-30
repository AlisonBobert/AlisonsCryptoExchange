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

const ltchost = "<LTCHOST>"
const ltcuser = "<LTCRPCUSER>"
const ltcpass = "<LTCRPCPASS>"
const ltcwalletName = "<LTCWALLET>"

var LtcBlockchainExplorers = []*CryptoTransactionExplorer{
	{
		Name:     "litecoinspace",
		IconPath: "asset_cache/litecoinspace.png",
		UrlResolver: func(s string) string {
			return "https://litecoinspace.org/tx/" + s
		},
	},
}

type LtcHandler struct {
	host      string
	user      string
	pass      string
	wallet    string
	client    *http.Client
	sendMutex sync.Mutex
}

func callGlobalLtcRPC(handler *LtcHandler, method string, params []interface{}) (map[string]interface{}, error) {
	requestBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "ltc-handler",
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

func NewLtcHandler() (*LtcHandler, error) {
	tempClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	handler := &LtcHandler{
		host:   ltchost,
		user:   ltcuser,
		pass:   ltcpass,
		wallet: ltcwalletName,
		client: tempClient,
	}
	result, err := callGlobalLtcRPC(handler, "listwallets", nil)
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
		_, err := callGlobalLtcRPC(handler, "loadwallet", []interface{}{handler.wallet})
		if err != nil {
			fmt.Println(err)
			if strings.Contains(err.Error(), "not found") {
				createParams := []interface{}{
					handler.wallet, // wallet name
					false,          // disable_private_keys
					false,          // blank
					"",             // passphrase
					true,           // avoid_reuse
				}
				_, createErr := callGlobalLtcRPC(handler, "createwallet", createParams)
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

func (h *LtcHandler) rpcWalletCall(method string, params []interface{}) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s/wallet/%s", h.host, h.wallet)
	requestBody := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "ltc-handler",
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

func (h *LtcHandler) CheckBalance() (float64, error) {
	result, err := h.rpcWalletCall("getbalance", []interface{}{"*", 1})
	if err != nil {
		return 0, err
	}

	balance, ok := result["result"].(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected response format from getbalance")
	}

	return balance, nil
}

func (h *LtcHandler) GenerateNewAddress() (CryptoAddress, error) {
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

func (h *LtcHandler) GetAddressTransaction(address CryptoAddress) (*CryptoTransaction, error) {
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
		return nil, fmt.Errorf("no transactions found for address after %d", address.StartTime)
	}

	sort.Slice(relevantTransactions, func(i, j int) bool {
		return relevantTransactions[i].Time < relevantTransactions[j].Time
	})

	newest := relevantTransactions[0]

	return &CryptoTransaction{
		Txid:          newest.Txid,
		Confirmations: newest.Confirmations,
		Amount:        newest.Amount,
		Explorers:     LtcBlockchainExplorers,
	}, nil
}

func (h *LtcHandler) GetTransactionDetails(txid string) (*CryptoTransaction, error) {
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
		Explorers:     LtcBlockchainExplorers,
	}, nil
}

func (h *LtcHandler) Send(address CryptoAddress, amount float64) ([]string, error) {
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
