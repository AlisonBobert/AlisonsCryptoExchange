package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"teProj/cryptoManager"
)

var handlers map[int64]cryptoManager.CryptoHandler

type CryptoCurrency struct {
	InternalAssetID      int    `json:"internalAssetID"`
	CoinmarketcapAssetID int    `json:"coinmarketcapAssetID"`
	AssetName            string `json:"assetName"`
	AddressRegex         string `json:"addressRegex"`
	AssetSign            string `json:"assetSign"`
	Precision            int    `json:"precision"`
	ConfirmationsNeeded  int    `json:"confirmationsNeeded"`
}

type Config struct {
	SupportedCryptos []CryptoCurrency `json:"supportedCryptos"`
	Routes           []struct {
		Pair struct {
			IDFrom int `json:"idFrom"`
			IDTo   int `json:"idTo"`
		} `json:"pair"`
		//Note to self change fee from absolute value to %
		Fee       float64 `json:"fee"`
		MinAmount float64 `json:"minAmount"`
	} `json:"routes"`
}

var config *Config

func loadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	Sessions = make(map[string]*ExchangeSession)
	//If session is completed successfully, delete it from memory
	CollectGarbage()
	var config Config
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return nil, err
	}
	handlers = make(map[int64]cryptoManager.CryptoHandler)
	for _, crypto := range config.SupportedCryptos {
		switch crypto.AssetName {
		case "Bitcoin":
			handler, err := cryptoManager.NewBtcHandler()
			if err != nil {
				continue
			}
			handlers[int64(crypto.InternalAssetID)] = handler
		case "Litecoin":
			handler, err := cryptoManager.NewLtcHandler()
			if err != nil {
				continue
			}
			handlers[int64(crypto.InternalAssetID)] = handler
		case "Monero":
			handler, err := cryptoManager.NewXmrHandler()
			if err != nil {
				continue
			}
			handlers[int64(crypto.InternalAssetID)] = handler
		case "Ethereum":
			handler, err := cryptoManager.NewEthHandler()
			if err != nil {
				continue
			}
			handlers[int64(crypto.InternalAssetID)] = handler
		default:
			continue
		}
	}

	return &config, nil
}

func downloadLogo(cacheDir string, internalID, cmcID int) error {
	url := fmt.Sprintf("https://s2.coinmarketcap.com/static/img/coins/64x64/%d.png", cmcID)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download logo: %s", resp.Status)
	}

	filePath := filepath.Join(cacheDir, fmt.Sprintf("%d.png", internalID))
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func cacheAssets(config *Config) error {
	cacheDir := "asset_cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	for _, crypto := range config.SupportedCryptos {
		filePath := filepath.Join(cacheDir, fmt.Sprintf("%d.png", crypto.InternalAssetID))
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			log.Printf("Downloading logo for %s...", crypto.AssetName)
			if err := downloadLogo(cacheDir, crypto.InternalAssetID, crypto.CoinmarketcapAssetID); err != nil {
				return err
			}
		}
	}
	return nil
}
