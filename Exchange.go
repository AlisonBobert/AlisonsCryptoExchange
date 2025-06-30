package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"teProj/cryptoManager"
	"time"
)

var sessionsMutex sync.RWMutex

var Sessions map[string]*ExchangeSession

var blankTransaction = cryptoManager.CryptoTransaction{Txid: "nil"}

type ExchangeSession struct {
	OrderID           string
	Status            string
	FromCurrency      cryptoManager.CryptoHandler
	ToCurrency        cryptoManager.CryptoHandler
	FromCurrencySign  string
	ToCurrencySign    string
	FromCurrencyID    int
	ToCurrencyID      int
	FeeRate           float64
	SendAmount        float64
	ReceiveAmount     float64
	ToAddress         string
	FromAddress       string
	RefundAddress     string
	ToTransactions    []cryptoManager.CryptoTransaction
	FromTransaction   cryptoManager.CryptoTransaction
	ToConfirmations   int
	FromConfirmations int
	ExchangeRate      float64
	ErrorMessage      string
	ExpirationTime    int64
	CollectionTime    int64
}

func CollectGarbage() {
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			sessionsMutex.Lock()
			for sessionID, session := range Sessions {
				if session.CollectionTime == -1 {
					continue
				}
				collectionTime := time.Unix(session.CollectionTime, 0)
				if time.Now().After(collectionTime) {
					delete(Sessions, sessionID)
				}
			}
			sessionsMutex.Unlock()
		}
	}()
}

func FormatExpirationTime(expirationTime int64) string {
	now := time.Now().Unix()
	remainingSeconds := expirationTime - now

	if remainingSeconds <= 0 {
		return "00:00" // Expired
	}

	minutes := remainingSeconds / 60
	seconds := remainingSeconds % 60

	if minutes < 0 {
		minutes = 0
	}
	if seconds < 0 {
		seconds = 0
	}

	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func MakeSession(fromID int, toID int, fromAmount, toAmount float64, toAddress, refundAddress string) (string, error) {
	buffer := make([]byte, 8)
	_, err := rand.Read(buffer)
	if err != nil {
		return "", fmt.Errorf("unable to generate new order id")
	}
	orderID := fmt.Sprintf("%x", buffer)
	fromHandler, ok := handlers[int64(fromID)]
	if !ok {
		return "", fmt.Errorf("invalid crypto (from)")
	}
	toHandler, ok := handlers[int64(toID)]
	if !ok {
		return "", fmt.Errorf("invalid crypto (to)")
	}
	var fromCurrencySign string = "nil"
	var toCurrencySign string = "nil"
	var toRegex string = "nil"
	var fromRegex string = "nil"
	var fromConf = -1
	var toConf = -1
	for _, crypto := range config.SupportedCryptos {
		if crypto.InternalAssetID == fromID {
			fromCurrencySign = crypto.AssetSign
			fromConf = crypto.ConfirmationsNeeded
			fromRegex = crypto.AddressRegex
		} else if crypto.InternalAssetID == toID {
			toCurrencySign = crypto.AssetSign
			toConf = crypto.ConfirmationsNeeded
			toRegex = crypto.AddressRegex
		}
	}
	if fromCurrencySign == "nil" || toCurrencySign == "nil" {
		return "", fmt.Errorf("invalid crypto sign")
	}

	if toRegex == "nil" || fromRegex == "nil" {
		return "", fmt.Errorf("invalid crypto address")
	}

	if fromConf == -1 || toConf == -1 {
		return "", fmt.Errorf("invalid crypto confirmations")
	}

	ok, err = regexp.MatchString(toRegex, toAddress)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf("invalid address")
	}

	ok, err = regexp.MatchString(fromRegex, refundAddress)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf("invalid address")
	}

	fee, ok := store.conversionFees[fmt.Sprintf("%d-%d", fromID, toID)]
	if !ok {
		return "", fmt.Errorf("route unavailable")
	}

	minAmount, ok := store.minAmounts[fmt.Sprintf("%d-%d", fromID, toID)]
	if !ok {
		return "", fmt.Errorf("route unavailable")
	}

	if fromAmount < minAmount {
		return "", fmt.Errorf("minimum amount %f %s", fromAmount, fromCurrencySign)
	}

	exchangeRate, err := ConvertWithoutFee(store, fromID, toID, 1)

	if err != nil {
		return "", fmt.Errorf("unable to calculate exchange rate")
	}

	tA, err := Convert(store, fromID, toID, fromAmount)

	if err != nil {
		return "", fmt.Errorf("unable to calculate to amount")
	}

	bal, err := toHandler.CheckBalance()

	if err != nil {
		return "", fmt.Errorf("unable to calculate balance")
	}

	if tA > bal {
		return "", fmt.Errorf("asking amount is higher then resources in the reserve")
	}

	session := ExchangeSession{
		OrderID:           orderID,
		Status:            "CREATED",
		FromCurrency:      fromHandler,
		ToCurrency:        toHandler,
		FromCurrencySign:  fromCurrencySign,
		ToCurrencySign:    toCurrencySign,
		FromCurrencyID:    fromID,
		ToCurrencyID:      toID,
		FeeRate:           fee * 100,
		SendAmount:        fromAmount,
		ReceiveAmount:     toAmount,
		ToAddress:         toAddress,
		RefundAddress:     refundAddress,
		FromAddress:       "",
		ToTransactions:    []cryptoManager.CryptoTransaction{blankTransaction},
		FromTransaction:   blankTransaction,
		ToConfirmations:   toConf,
		FromConfirmations: fromConf,
		ExchangeRate:      exchangeRate,
		CollectionTime:    -1,
		ExpirationTime:    time.Now().Add(15 * time.Minute).Unix(),
	}
	sessionsMutex.Lock()
	Sessions[orderID] = &session
	sessionsMutex.Unlock()
	return orderID, nil
}

const key = "<ENCRYPTIONKEY>"

func EncryptInternalMessage(err error) string {
	key, _ := hex.DecodeString(key)
	plaintext := []byte(err.Error())
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return ""
	}
	ciphertext := aesGCM.Seal(nonce, nonce, plaintext, nil)
	return " Internal Error: " + base64.StdEncoding.EncodeToString(ciphertext)
}

func ExchangeBackend(session *ExchangeSession) error {
	LogActivity("New Order Created, %#v ", *session)
	address, err := session.FromCurrency.GenerateNewAddress()
	if err != nil {
		LogError("Order failed with error: %s, %#v", err.Error(), *session)
		session.Status = "TRANSLATION FAILED"
		session.ErrorMessage = "Unable to generate new address." + EncryptInternalMessage(err)
		return err
	}
	LogActivity("Address successfully created %s awaiting input, %#v", address, *session)
	session.Status = "AWAITING INPUT"
	session.FromAddress = address.Address

	var fromTransaction cryptoManager.CryptoTransaction
	for {
		if time.Now().After(time.Unix(session.ExpirationTime, 0)) {
			LogError("Order expired, %#v", *session)
			session.Status = "TRANSLATION FAILED"
			session.ErrorMessage = "Transaction Expired"
			return fmt.Errorf("transaction Expired")
		}

		transaction, err := session.FromCurrency.GetAddressTransaction(address)

		if err != nil && !strings.Contains(err.Error(), "no transactions found") {

			continue

		}
		if transaction != nil {
			fromTransaction = *transaction
			break
		}
		receiveAmount, err := Convert(store, session.FromCurrencyID, session.ToCurrencyID, session.SendAmount)
		if err == nil {
			session.ReceiveAmount = receiveAmount
		}

		exchangeRate, err := ConvertWithoutFee(store, session.FromCurrencyID, session.ToCurrencyID, 1)

		if err == nil {
			session.ReceiveAmount = exchangeRate
		}

		time.Sleep(5 * time.Second)
	}
	LogActivity("Received %f %s at address %s confirming input, %#v", fromTransaction.Amount, session.FromCurrencySign, address, *session)
	session.Status = "CONFIRMING INPUT"
	session.FromTransaction = fromTransaction
	session.ReceiveAmount = fromTransaction.Amount
	sendAmount, err := Convert(store, session.FromCurrencyID, session.ToCurrencyID, fromTransaction.Amount)
	if err != nil {
		LogError("Order failed with error: %s, %#v", err.Error(), *session)
		session.Status = "TRANSLATION FAILED"
		session.ErrorMessage = "Unable to calculate amount to send." + EncryptInternalMessage(err)
		return err
	}
	session.SendAmount = sendAmount
	currentConfirm := session.FromTransaction.Confirmations
	for int(currentConfirm) < session.FromConfirmations {
		time.Sleep(5 * time.Second)
		transaction, err := session.FromCurrency.GetTransactionDetails(session.FromTransaction.Txid)
		if err != nil {
			continue
		}
		currentConfirm = transaction.Confirmations
		session.FromTransaction = *transaction
	}
	LogActivity("Incoming transaction %s confirmed %d times, exchanging, %#v", session.FromTransaction.Txid, session.FromConfirmations, *session)
	session.Status = "EXCHANGING"
	toTxid, err := session.ToCurrency.Send(cryptoManager.CryptoAddress{
		Address:   session.ToAddress,
		StartTime: 0,
	}, session.SendAmount)
	if err != nil {
		LogError("Order failed with error: %s, %#v", err.Error(), *session)
		session.Status = "TRANSLATION FAILED"
		session.ErrorMessage = "Unable to exchange funds." + EncryptInternalMessage(err)
		return err
	}
	time.Sleep(5 * time.Second)
	var transactions []cryptoManager.CryptoTransaction
	for _, tTxid := range toTxid {
		var transaction *cryptoManager.CryptoTransaction
		for i := 0; i < 3; i++ {
			transaction, err = session.ToCurrency.GetTransactionDetails(tTxid)
			if err == nil {
				break
			}
			time.Sleep(5 * time.Second)
		}
		if err != nil {
			LogError("Order failed with error: %s, %#v", err.Error(), *session)
			session.Status = "TRANSLATION FAILED"
			session.ErrorMessage = "Unable to fetch output transaction details." + EncryptInternalMessage(err)
			return err
		}
		transactions = append(transactions, *transaction)
	}
	LogActivity("Funds exchanged successfully output transactions [%v], %#v ", toTxid, *session)
	session.Status = "CONFIRMING OUTPUT"
	session.ToTransactions = transactions
	var currentConfirms []int64
	for _, transaction := range transactions {
		currentConfirms = append(currentConfirms, transaction.Confirmations)
	}
	var areAllConfirmed bool = true
	for {
		for _, confirm := range currentConfirms {
			areAllConfirmed = areAllConfirmed && (int(confirm) >= session.ToConfirmations)
		}
		if areAllConfirmed {
			break
		}
		areAllConfirmed = true
		transactions = make([]cryptoManager.CryptoTransaction, 0)
		for _, tTxid := range toTxid {
			var transaction *cryptoManager.CryptoTransaction
			for i := 0; i < 3; i++ {
				transaction, err = session.ToCurrency.GetTransactionDetails(tTxid)
				if err == nil {
					break
				}
				time.Sleep(5 * time.Second)
			}
			if err != nil {
				LogError("Order failed with error: %s, %#v", err.Error(), *session)
				session.Status = "TRANSLATION FAILED"
				session.ErrorMessage = "Unable to fetch output transaction details." + EncryptInternalMessage(err)
				return err
			}
			transactions = append(transactions, *transaction)
		}
		session.ToTransactions = transactions
		currentConfirms = make([]int64, 0)
		for _, transaction := range transactions {
			currentConfirms = append(currentConfirms, transaction.Confirmations)
		}
		time.Sleep(5 * time.Second)
	}
	LogActivity("Order completed successfully, %#v", *session)
	session.Status = "SUCCESS"
	session.CollectionTime = time.Now().Add(1 * time.Hour).Unix()
	return nil
}
