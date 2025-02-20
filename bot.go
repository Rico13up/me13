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
	client        = horizonclient.DefaultTestNetClient
	networkPass   = network.TestNetworkPassphrase
	running       = false
	testSecretKey = ""
)

func main() {
	a := app.New()
	w := a.NewWindow("Stellar Arbitrage Bot")
	w.Resize(fyne.NewSize(600, 400))

	secretEntry := widget.NewEntry()
	secretEntry.SetPlaceHolder("Enter your secret key (testnet)")

	volumeEntry := widget.NewEntry()
	volumeEntry.SetPlaceHolder("Enter transaction volume (e.g., 10 XLM)")
	volumeEntry.SetText("10")

	logText := widget.NewLabel("Bot log will appear here...\n")

	startBtn := widget.NewButton("Start", func() {
		if !running {
			running = true
			go runBot(secretEntry.Text, volumeEntry.Text, logText)
		}
	})

	stopBtn := widget.NewButton("Stop", func() {
		running = false
		logText.SetText(logText.Text + "Bot stopped.\n")
	})

	content := container.NewVBox(
		widget.NewLabel("Stellar Arbitrage Bot"),
		secretEntry,
		volumeEntry,
		container.NewHBox(startBtn, stopBtn),
		logText,
	)
	w.SetContent(content)

	kp, err := keypair.Random()
	if err != nil {
		log.Fatal(err)
	}
	testSecretKey = kp.Seed()
	logText.SetText(fmt.Sprintf("Generated test key: %s\nPublic key: %s\n", testSecretKey, kp.Address()))

	w.ShowAndRun()
}

func runBot(secretKey, volume string, logText *widget.Label) {
	if secretKey == "" {
		logText.SetText(logText.Text + "Error: Please enter a secret key.\n")
		return
	}

	kp, err := keypair.Parse(secretKey)
	if err != nil {
		logText.SetText(logText.Text + "Error: Invalid secret key.\n")
		return
	}

	volumeFloat, err := strconv.ParseFloat(volume, 64)
	if err != nil || volumeFloat <= 0 {
		logText.SetText(logText.Text + "Error: Invalid volume.\n")
		return
	}

	err = requestFriendbot(kp.Address())
	if err != nil {
		logText.SetText(logText.Text + fmt.Sprintf("Error funding account: %v\n", err))
		return
	}
	logText.SetText(logText.Text + "Account funded with 10,000 XLM in testnet.\n")

	err = setupTrustlines(kp)
	if err != nil {
		logText.SetText(logText.Text + fmt.Sprintf("Error setting trustlines: %v\n", err))
		return
	}
	logText.SetText(logText.Text + "Trustlines for USDC and yXLM set.\n")

	for running {
		logText.SetText(logText.Text + "Scanning for arbitrage opportunities...\n")
		err := findAndExecuteArbitrage(kp, volume, logText)
		if err != nil {
			logText.SetText(logText.Text + fmt.Sprintf("Error: %v\n", err))
		}
		time.Sleep(1 * time.Second)
	}
}

func requestFriendbot(address string) error {
	_, err := horizonclient.DefaultTestNetClient.Fund(address)
	return err
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
	if profit <= cost+0.00001 {
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
