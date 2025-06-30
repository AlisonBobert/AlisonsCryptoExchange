package cryptoManager

import "math"

type CryptoAddress struct {
	Address   string
	StartTime int64
}

type CryptoTransactionExplorer struct {
	Name        string
	IconPath    string
	UrlResolver func(string) string
}

type CryptoTransaction struct {
	Txid          string
	Confirmations int64
	Amount        float64
	Explorers     []*CryptoTransactionExplorer
}

type CryptoHandler interface {
	GenerateNewAddress() (CryptoAddress, error)
	CheckBalance() (float64, error)
	GetAddressTransaction(address CryptoAddress) (*CryptoTransaction, error)
	GetTransactionDetails(txid string) (*CryptoTransaction, error)
	Send(address CryptoAddress, amount float64) ([]string, error)
}

func RoundToNDigits(val float64, n int) float64 {
	pow := math.Pow(10, float64(n))
	return math.Round(val*pow) / pow
}
