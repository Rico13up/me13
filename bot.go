package main

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/network"
	"github.com/stellar/go/txnbuild"
)

var (
	client      = horizonclient.DefaultPublicNetClient
	networkPass = network.PublicNetworkPassphrase
	running     = false
)

func main() {
	// Используем твой реальный секретный ключ
	secretKey := "SBRUEQOKCI7U7GMTPFWSGTN6SVYAMGGJUERBWVRFP2ZAY4M3URYAU3FR"
	kp, err := keypair.Parse(secretKey)
	if err != nil {
		log.Fatal("Invalid secret key:", err)
	}

	a := app.New()
	w := a.NewWindow("Stellar Arbitrage Bot (MainNet)")
	w.Resize(fyne.NewSize(600, 400))

	// Поле для объёма транзакции
	volumeEntry := widget.NewEntry()
	volumeEntry.SetPlaceHolder("Enter transaction volume (e.g., 10 XLM)")
	volumeEntry.SetText("10") // Значение по умолчанию

	// Лог операций
	logText := widget.NewLabel("Bot log will appear here...\n")

	// Кнопка старта
	startBtn := widget.NewButton("Start", func() {
		if !running {
			running = true
			go runBot(kp, volumeEntry.Text, logText)
		}
	})

	// Кнопка остановки
	stopBtn := widget.NewButton("Stop", func() {
		running = false
		logText.SetText(logText.Text + "Bot stopped.\n")
	})

	// Интерфейс
	content := container.NewVBox(
		widget.NewLabel("Stellar Arbitrage Bot (MainNet)"),
		volumeEntry,
		container.NewHBox(startBtn, stopBtn),
		logText,
	)
	w.SetContent(content)

	// Проверка аккаунта (в основной сети нет Friendbot, поэтому проверяем баланс)
	account, err := client.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		logText.SetText(fmt.Sprintf("Error: Account not found or insufficient funds. Ensure account has XLM: %v\n", err))
	} else {
		logText.SetText(fmt.Sprintf("Account found with %s XLM.\n", account.Balances[0].Balance))
	}

	// Настройка trustlines для USDC и yXLM
	err = setupTrustlines(kp)
	if err != nil {
		logText.SetText(fmt.Sprintf("Error setting trustlines: %v\n", err))
	} else {
		logText.SetText("Trustlines for USDC and yXLM set.\n")
	}

	w.ShowAndRun()
}

func runBot(kp *keypair.Full, volume string, logText *widget.Label) {
	volumeFloat, err := strconv.ParseFloat(volume, 64)
	if err != nil || volumeFloat <= 0 {
		logText.SetText(logText.Text + "Error: Invalid volume.\n")
		return
	}

	for running {
		logText.SetText(logText.Text + "Scanning for arbitrage opportunities...\n")
		err := findAndExecuteArbitrage(kp, volume, logText)
		if err != nil {
			logText.SetText(logText.Text + fmt.Sprintf("Error: %v\n", err))
		}
		time.Sleep(1 * time.Second)
	}
}

func setupTrustlines(kp *keypair.Full) error {
	accountRequest := horizonclient.AccountRequest{AccountID: kp.Address()}
	account, err := client.AccountDetail(accountRequest)
	if err != nil {
		return err
	}

	usdc := txnbuild.CreditAsset{Code: "USDC", Issuer: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}
	yxlm := txnbuild.CreditAsset{Code: "yXLM", Issuer: "GARDNV3Q7YGT4AKSDF25LT32YSCCW4EV22Y2TV3I2PU2MMXJTEDL5T55"}

	tx, err := txnbuild.NewTransaction(
		txnbuild.TransactionParams{
			SourceAccount:        &account,
			IncrementSequenceNum: true,
			Operations: []txnbuild.Operation{
				&txnbuild.ChangeTrust{Line: usdc, Limit: "10000"},
				&txnbuild.ChangeTrust{Line: yxlm, Limit: "10000"},
			},
			BaseFee:    txnbuild.MinBaseFee,
			Timebounds: txnbuild.NewTimeout(300),
			Network:    networkPass,
		},
	)
	if err != nil {
		return err
	}

	tx, err = tx.Sign(networkPass, kp)
	if err != nil {
		return err
	}

	_, err = client.SubmitTransaction(tx)
	return err
}

func findAndExecuteArbitrage(kp *keypair.Full, volume string, logText *widget.Label) error {
	usdcAsset := txnbuild.CreditAsset{Code: "USDC", Issuer: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}
	yxlmAsset := txnbuild.CreditAsset{Code: "yXLM", Issuer: "GARDNV3Q7YGT4AKSDF25LT32YSCCW4EV22Y2TV3I2PU2MMXJTEDL5T55"}
	xlmAsset := txnbuild.NativeAsset{}

	paths, err := client.Paths(horizonclient.PathsRequest{
		DestinationAccount: kp.Address(),
		DestinationAsset:   xlmAsset,
		DestinationAmount:  volume,
		SourceAssets:       []txnbuild.Asset{xlmAsset},
		SourceAmount:       volume,
	})
	if err != nil {
		return err
	}

	if len(paths.Records) == 0 {
		logText.SetText(logText.Text + "No arbitrage opportunities found.\n")
		return nil
	}

	path := paths.Records[0]
	logText.SetText(logText.Text + fmt.Sprintf("Found path: %s XLM -> %s %s -> %s XLM\n", path.SourceAmount, path.DestinationAmount, path.Path[0].Code, path.DestinationAmount))

	profit, _ := strconv.ParseFloat(path.DestinationAmount, 64)
	cost, _ := strconv.ParseFloat(path.SourceAmount, 64)
	if profit <= cost+0.00001 { // Учитываем комиссию (0.00001 XLM в основной сети)
		logText.SetText(logText.Text + "No profit in this path.\n")
		return nil
	}

	account, err := client.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		return err
	}

	tx, err := txnbuild.NewTransaction(
		txnbuild.TransactionParams{
			SourceAccount:        &account,
			IncrementSequenceNum: true,
			Operations: []txnbuild.Operation{
				&txnbuild.PathPaymentStrictReceive{
					SendAsset:   xlmAsset,
					SendMax:     volume,
					DestAsset:   xlmAsset,
					DestAmount:  path.DestinationAmount,
					Destination: kp.Address(),
					Path:        []txnbuild.Asset{usdcAsset, yxlmAsset},
				},
			},
			BaseFee:    txnbuild.MinBaseFee,
			Timebounds: txnbuild.NewTimeout(300),
			Network:    networkPass,
		},
	)
	if err != nil {
		return err
	}

	tx, err = tx.Sign(networkPass, kp)
	if err != nil {
		return err
	}

	_, err = client.SubmitTransaction(tx)
	if err != nil {
		return err
	}

	logText.SetText(logText.Text + fmt.Sprintf("Arbitrage executed! Profit: %s XLM\n", fmt.Sprintf("%.6f", profit-cost-0.00001)))
	return nil
}
