package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

const (
	TFT_ISSUER     = "GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47"
	TFT_ASSET_CODE = "TFT"

	TRANSACTIONS_PER_PAGE = 100
	HORIZON_PAGE_LIMIT    = 200

	BASE_FEE                  = 1_000_000 // 0.1XLM
	TXN_VALIDITY_TIME_SECONDS = 60 * 60 * 24 * 6
)

var (
	sequenceNumber int64
	payoutFile     string
	outputFile     string
	checkTrust     bool
)

func init() {
	flag.Int64Var(&sequenceNumber, "sequence_number", 0, "Sets the sequence number to use")
	flag.StringVar(&payoutFile, "payoutsfile", "payout_info.csv", "The input csv file")
	flag.StringVar(&outputFile, "outputfile", "payouts_to_sign.txt", "The output file to send around")
	flag.BoolVar(&checkTrust, "check_trust", true, "whether trustlines should be checked for destinations")
}

func main() {
	flag.Parse()

	if sequenceNumber == 0 {
		panic("Sequence number is required")
	}

	cl := horizonclient.DefaultPublicNetClient

	knownMemos, err := getMemoHashes(cl, TFT_ISSUER)
	if err != nil {
		panic(fmt.Sprintf("Failed to list known memos %s", err))
	}
	fmt.Println("Got a list of all memos")

	payoutFile, err := os.Open(payoutFile)
	if err != nil {
		panic(fmt.Sprintf("Failed to open payouts input file %s", err))
	}
	reader := bufio.NewReader(payoutFile)
	defer payoutFile.Close()

	outFile, err := os.Create(outputFile)
	if err != nil {
		panic(fmt.Sprintf("Failed to open payouts output file %s", err))
	}
	defer outFile.Close()
	defer outFile.Sync()

	var trustlineVerifier TFTTrustLineChecker
	if checkTrust {
		trustlineVerifier = newHorizonChecker(cl)
	} else {
		trustlineVerifier = NOPChecker{}
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			panic(err)
		}
		if line == "" {
			break
		}

		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) != 3 {
			panic("Invalid file layout")
		}

		target := strings.TrimSpace(parts[0])
		amount := strings.TrimSpace(parts[1])
		memo := strings.TrimSpace(parts[2])

		for _, knownMemo := range knownMemos {
			if knownMemo == memo {
				fmt.Println("ERROR: Payment of", amount, "TFT to", target, "with memo", memo, "already happened")
				continue
			}
		}

		hasTrustline, err := trustlineVerifier.TrustLineCheck(target)
		if err != nil {
			panic(fmt.Sprintf("Could not check trustline %s", err))
		}

		if !hasTrustline {
			fmt.Println("ERROR:", target, "has no trusltine for TFT")
			continue
		}

		memoBytes, err := hex.DecodeString(memo)
		if err != nil {
			panic(fmt.Sprintf("Memo %s is not valid hex", memo))
		}
		memoHash := txnbuild.MemoHash{}
		copy(memoHash[:], memoBytes)

		fmt.Println("Sending", amount, "to", target, "with memo", memo)

		paymentOp := txnbuild.Payment{
			Destination:   target,
			Amount:        amount,
			Asset:         txnbuild.CreditAsset{Code: TFT_ASSET_CODE, Issuer: TFT_ISSUER},
			SourceAccount: TFT_ISSUER,
		}

		if err = paymentOp.Validate(); err != nil {
			fmt.Println("ERROR: Could not construct payment to", target, err)
			continue
		}

		params := txnbuild.TransactionParams{
			SourceAccount:        &txnbuild.SimpleAccount{AccountID: TFT_ISSUER, Sequence: sequenceNumber},
			IncrementSequenceNum: true,
			Operations:           []txnbuild.Operation{&paymentOp},
			BaseFee:              BASE_FEE,
			Memo:                 memoHash,
			Preconditions: txnbuild.Preconditions{
				TimeBounds: txnbuild.NewTimeout(TXN_VALIDITY_TIME_SECONDS),
			},
		}

		tx, err := txnbuild.NewTransaction(params)
		if err != nil {
			fmt.Println("ERROR: Failed to generate minting transaction", err)
		}

		sequenceNumber = tx.SequenceNumber()

		xdr, err := tx.MarshalText()
		if err != nil {
			panic(err)
		}
		outFile.WriteString(string(xdr))
		outFile.WriteString("\n")
	}

}

func getMemoHashes(cl *horizonclient.Client, account string) ([]string, error) {
	cursor := ""

	memos := []string{}

	for {
		opReq := horizonclient.OperationRequest{
			ForAccount: account,
			Cursor:     cursor,
			Limit:      HORIZON_PAGE_LIMIT,
			Join:       "transactions",
		}
		ops, err := cl.Operations(opReq)
		if err != nil {
			e := err.(*horizonclient.Error)
			if e.Response.StatusCode == 500 {
				time.Sleep(time.Second)
				continue
			}
			fmt.Println(e.Problem)
			return nil, err
		}

		if len(ops.Embedded.Records) == 0 {
			break
		}

		cursor = ops.Embedded.Records[len(ops.Embedded.Records)-1].PagingToken()
		for _, op := range ops.Embedded.Records {
			if payment, ok := op.(operations.Payment); ok {
				if payment.From != account {
					continue
				}
				memo := ""
				if payment.Transaction != nil {
					if payment.Transaction.MemoType != "hash" {
						// All minting txes have a "hash" memo type
						continue
					}
					raw, err := base64.StdEncoding.DecodeString(payment.Transaction.Memo)
					if err != nil {
						return nil, err
					}
					memo = hex.EncodeToString(raw)
					memos = append(memos, memo)
				}
			}
		}
		if len(ops.Embedded.Records) < 200 {
			break
		}
	}

	return memos, nil
}

type TFTTrustLineChecker interface {
	// TurstLinceCheck for a TFT trustline on an account
	TrustLineCheck(account string) (bool, error)
}

type NOPChecker struct {
}

func (n NOPChecker) TrustLineCheck(_ string) (bool, error) {
	return true, nil
}

type HorizonChecker struct {
	client         *horizonclient.Client
	knownAddresses map[string]bool
}

func newHorizonChecker(cl *horizonclient.Client) *HorizonChecker {
	return &HorizonChecker{
		client:         cl,
		knownAddresses: make(map[string]bool),
	}
}
func (hc *HorizonChecker) TrustLineCheck(account string) (bool, error) {
	if res, exists := hc.knownAddresses[account]; exists {
		return res, nil
	}

	for {
		req := horizonclient.AccountRequest{
			AccountID: account,
		}

		acc, err := hc.client.AccountDetail(req)
		if err != nil {
			if err, ok := err.(*horizonclient.Error); ok {
				if err.Response.StatusCode == 500 {
					time.Sleep(time.Second)
					continue
				}
				fmt.Println("ERROR checking account", err.Problem)
				// Mask horizon errors
				return false, nil
			}
			return false, errors.Wrap(err, "failed to get account data")
		}

		for _, balance := range acc.Balances {
			if balance.Asset.Code == TFT_ASSET_CODE && balance.Asset.Issuer == TFT_ISSUER {
				hc.knownAddresses[account] = true
				return true, nil
			}
		}

		break
	}

	return false, nil
}
