package cryptoManager

import (
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"math/big"
	"math/rand"
	"sync"
	"time"
)

const ethhost = "<ETHNODEURL>"

var EthBlockchainExplorers = []*CryptoTransactionExplorer{
	{
		Name:     "etherscan",
		IconPath: "asset_cache/etherscan.png",
		UrlResolver: func(s string) string {
			return "https://etherscan.io/tx/" + s
		},
	},
}

type EthHandler struct {
	ethKeystore      *keystore.KeyStore
	ehtClient        *ethclient.Client
	sendMutex        sync.Mutex
	transactionCache map[string]int64
}

func getCurrentEthBlock(h *EthHandler) (int64, error) {
	number, err := h.ehtClient.HeaderByNumber(context.Background(), nil)
	if err != nil && number.Number != nil {
		return 0, err
	}
	return number.Number.Int64(), nil
}

func getAccountList(handler *EthHandler) ([]string, error) {
	accounts := handler.ethKeystore.Accounts()
	strAccounts := make([]string, 0)
	for _, account := range accounts {
		strAccounts = append(strAccounts, account.Address.Hex())
	}
	return strAccounts, nil

}

func getAccountBalance(handler *EthHandler, account string) (*big.Int, error) {
	addressRaw := common.HexToAddress(account)
	balanceWei, err := handler.ehtClient.BalanceAt(context.Background(), addressRaw, nil)
	if err != nil {
		return nil, err
	}
	return balanceWei, nil
}

func NewEthHandler() (*EthHandler, error) {
	client, err := ethclient.Dial(ethhost)
	if err != nil {
		return nil, err
	}
	handler := &EthHandler{
		ehtClient:        client,
		ethKeystore:      keystore.NewKeyStore("ethKeystore", keystore.StandardScryptN, keystore.StandardScryptP),
		transactionCache: make(map[string]int64),
	}
	return handler, nil
}

func (h *EthHandler) CheckBalance() (float64, error) {
	accounts, err := getAccountList(h)
	if err != nil {
		return 0, err
	}

	var totalBalanceWei *big.Int = big.NewInt(0)
	oneEthInWei := big.NewInt(1e18)

	for _, account := range accounts {
		balanceWei, err := getAccountBalance(h, account)
		if err != nil {
			return 0, err
		}
		totalBalanceWei.Add(totalBalanceWei, balanceWei)
	}

	totalBalanceEth := new(big.Float).SetInt(totalBalanceWei)
	totalBalanceEth.Quo(totalBalanceEth, new(big.Float).SetInt(oneEthInWei))
	balance, _ := totalBalanceEth.Float64()

	return balance, nil
}

func (h *EthHandler) GenerateNewAddress() (CryptoAddress, error) {
	account, err := h.ethKeystore.NewAccount("<ETHKEYSTOREPASS>")
	if err != nil {
		return CryptoAddress{}, err
	}
	currentBlock, err := getCurrentEthBlock(h)
	if err != nil {
		return CryptoAddress{}, err
	}
	return CryptoAddress{
		Address:   account.Address.Hex(),
		StartTime: currentBlock,
	}, nil

}

func (h *EthHandler) GetAddressTransaction(address CryptoAddress) (*CryptoTransaction, error) {
	if !common.IsHexAddress(address.Address) {
		return nil, fmt.Errorf("invalid Ethereum address")
	}
	ethAddress := common.HexToAddress(address.Address)
	currentBlock, err := getCurrentEthBlock(h)
	if err != nil {
		return nil, err
	}
	var startBlock *big.Int
	if cachedBlock, exists := h.transactionCache[address.Address]; exists {
		startBlock = big.NewInt(cachedBlock + 1)
	} else {
		startBlock = big.NewInt(address.StartTime)
	}

	currentBlockBigInt := big.NewInt(currentBlock)
	bigInt1 := big.NewInt(1)

	for blockNum := startBlock; blockNum.Cmp(currentBlockBigInt) <= 0; blockNum.Add(blockNum, bigInt1) {
		block, err := h.ehtClient.BlockByNumber(context.Background(), blockNum)
		if err != nil {
			return nil, err
		}
		for _, tx := range block.Transactions() {
			if tx.ChainId().Int64() != 1 {
				continue
			}
			sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
			if err != nil {
				continue
			}
			if sender == ethAddress || (tx.To() != nil && *tx.To() == ethAddress) {
				delete(h.transactionCache, address.Address)
				confirmations := new(big.Int).Sub(currentBlockBigInt, block.Number()).Int64()
				amount := new(big.Float).Quo(
					new(big.Float).SetInt(tx.Value()),
					new(big.Float).SetInt(big.NewInt(1e18)),
				).SetPrec(64)
				amountFloat, _ := amount.Float64()

				return &CryptoTransaction{
					Txid:          tx.Hash().Hex(),
					Confirmations: confirmations,
					Amount:        amountFloat,
					Explorers:     EthBlockchainExplorers,
				}, nil
			}
		}
	}
	h.transactionCache[address.Address] = currentBlockBigInt.Int64()
	return nil, nil
}

func (h *EthHandler) GetTransactionDetails(txid string) (*CryptoTransaction, error) {
	txHash := common.HexToHash(txid)

	tx, isPending, err := h.ehtClient.TransactionByHash(context.Background(), txHash)
	if err != nil {
		return nil, err
	}

	receipt, err := h.ehtClient.TransactionReceipt(context.Background(), txHash)
	if err != nil {
		return nil, err
	}

	currentHeader, err := h.ehtClient.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	var confirmations int64
	if !isPending && receipt != nil {
		confirmations = new(big.Int).Sub(currentHeader.Number, receipt.BlockNumber).Int64()
	}

	amount := new(big.Float).Quo(
		new(big.Float).SetInt(tx.Value()),
		new(big.Float).SetInt(big.NewInt(1e18)),
	)
	amountFloat, _ := amount.Float64()

	return &CryptoTransaction{
		Txid:          txHash.Hex(),
		Confirmations: confirmations,
		Amount:        amountFloat,
		Explorers:     EthBlockchainExplorers,
	}, nil
}

func sendFromAccount(h *EthHandler, account accounts.Account, toAddress common.Address, amountWei, gasPrice *big.Int, gasLimit uint64) ([]string, error) {
	err := h.ethKeystore.Unlock(account, "<ETHKEYSTOREPASS>")
	if err != nil {
		return nil, err
	}
	defer h.ethKeystore.Lock(account.Address) // Lock when done

	nonce, err := h.ehtClient.PendingNonceAt(context.Background(), account.Address)
	if err != nil {
		return nil, err
	}
	fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))

	tx := types.NewTransaction(
		nonce,
		toAddress,
		new(big.Int).Sub(amountWei, fee),
		gasLimit,
		gasPrice,
		nil,
	)

	signedTx, err := h.ethKeystore.SignTx(account, tx, big.NewInt(1))
	if err != nil {
		return nil, err
	}

	time.Sleep(5 * time.Second)

	err = h.ehtClient.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, err
	}

	return []string{signedTx.Hash().Hex()}, nil
}

func sendFromMultipleAccounts(h *EthHandler, toAddress common.Address, amountWei, gasPrice *big.Int, gasLimit uint64) ([]string, error) {
	type accountBalance struct {
		account accounts.Account
		balance *big.Int
	}
	var accountsWithBalances []accountBalance
	totalBalance := new(big.Int)
	fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))

	for _, acc := range h.ethKeystore.Accounts() {
		balance, err := h.ehtClient.BalanceAt(context.Background(), acc.Address, nil)
		if err != nil {
			continue
		}
		accountsWithBalances = append(accountsWithBalances, accountBalance{
			account: acc,
			balance: balance,
		})
		totalBalance.Add(totalBalance, balance)
	}

	totalNeeded := new(big.Int).Sub(amountWei, fee)
	if totalBalance.Cmp(totalNeeded) < 0 {
		return nil, fmt.Errorf("insufficient funds")
	}

	var txHashes []string
	remaining := new(big.Int).Set(amountWei)
	for _, accBal := range accountsWithBalances {
		if remaining.Sign() <= 0 {
			break
		}
		sendAmount := new(big.Int).Set(accBal.balance)
		if sendAmount.Cmp(remaining) > 0 {
			sendAmount.Set(remaining)
		}
		txHash, err := sendFromAccount(h, accBal.account, toAddress, sendAmount, gasPrice, gasLimit)
		if err != nil {
			return nil, err
		}
		txHashes = append(txHashes, txHash[0])
		remaining.Sub(remaining, sendAmount)
	}
	if len(txHashes) > 0 {
		return txHashes, nil
	}
	return nil, fmt.Errorf("no transactions were sent")
}

func consolidateSmallBalances(h *EthHandler, gasPrice *big.Int, gasLimit uint64) error {
	type accountBalance struct {
		account accounts.Account
		balance *big.Int
	}

	fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	tenTimesFee := new(big.Int).Mul(big.NewInt(10), fee)
	var smallAccounts []accountBalance
	var largeAccounts []accounts.Account
	for _, acc := range h.ethKeystore.Accounts() {
		balance, err := h.ehtClient.BalanceAt(context.Background(), acc.Address, nil)
		if err != nil {
			continue
		}

		if balance.Cmp(tenTimesFee) < 0 {
			smallAccounts = append(smallAccounts, accountBalance{account: acc, balance: balance})
		} else {
			largeAccounts = append(largeAccounts, acc)
		}
	}
	if len(largeAccounts) == 0 {
		return nil
	}
	for _, smallAcc := range smallAccounts {
		if smallAcc.balance.Cmp(fee) <= 0 {
			continue
		}

		targetAccount := largeAccounts[rand.Intn(len(largeAccounts))]

		sendAmount := new(big.Int).Sub(smallAcc.balance, fee)

		_, err := sendFromAccount(h, smallAcc.account, targetAccount.Address, sendAmount, gasPrice, gasLimit)
		if err != nil {
			continue
		}
	}
	return nil
}

func (h *EthHandler) Send(address CryptoAddress, amount float64) ([]string, error) {
	h.sendMutex.Lock()
	defer h.sendMutex.Unlock()
	if !common.IsHexAddress(address.Address) {
		return nil, fmt.Errorf("invalid recipient address")
	}
	toAddress := common.HexToAddress(address.Address)
	amountWei := new(big.Int)
	amountFloat := big.NewFloat(amount)
	amountFloat.Mul(amountFloat, big.NewFloat(1e18))
	amountFloat.Int(amountWei)
	gasPrice, err := h.ehtClient.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, err
	}
	gasLimit := uint64(21000)
	totalCost := amountWei

	var sufficientAccounts []accounts.Account
	h.ethKeystore.Accounts()
	for _, acc := range h.ethKeystore.Accounts() {
		balance, err := h.ehtClient.BalanceAt(context.Background(), acc.Address, nil)
		if err != nil {
			continue
		}

		if balance.Cmp(totalCost) >= 0 {
			sufficientAccounts = append(sufficientAccounts, acc)
		}
	}
	if len(sufficientAccounts) > 0 {
		return sendFromAccount(h, sufficientAccounts[0], toAddress, amountWei, gasPrice, gasLimit)
	}
	return sendFromMultipleAccounts(h, toAddress, amountWei, gasPrice, gasLimit)
}
