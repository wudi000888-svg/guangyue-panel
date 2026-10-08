package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

var errCryptoSweepIntegrity = errors.New("crypto sweep integrity failed")

// A signed transaction proves authorization, not execution. Final states must
// have authenticated evidence written only after both RPCs verified finality.
func (s *Store) validateCryptoSweepReceipt(v cryptoChainTransaction, block uint64, hash string, sealed []byte) error {
	if v.State != "confirmed" && v.State != "reverted" {
		if (v.State != "signed" && v.State != "pending") || block != 0 || hash != "" || len(sealed) != 0 {
			return errCryptoSweepIntegrity
		}
		return nil
	}
	var proof CryptoSweepReceiptProof
	if len(sealed) == 0 || s.vault.open(sealed, &proof) != nil || proof.TransactionID != v.ID || proof.JobID != v.JobID || proof.ItemID != v.ItemID || proof.ChainID != v.ChainID {
		return errCryptoSweepIntegrity
	}
	r := proof.Receipt
	to := v.To
	if v.Token != "" {
		to = v.Token
	}
	if r.BlockNumber != block || r.BlockHash != hash || !cryptoHash(hash) || !strings.EqualFold(r.TxHash, v.Hash) || !strings.EqualFold(r.From, v.From) || !strings.EqualFold(r.To, to) || r.Status > 1 || (v.State == "confirmed") != (r.Status == 1) {
		return errCryptoSweepIntegrity
	}
	seen := map[uint64]bool{}
	count := 0
	for _, log := range r.Logs {
		if log.Removed || log.TxHash != r.TxHash || log.BlockHash != r.BlockHash || log.BlockNumber != block || seen[log.Index] {
			return errCryptoSweepIntegrity
		}
		seen[log.Index] = true
		if r.Status == 1 && v.Token != "" && strings.EqualFold(log.Address, v.Token) && len(log.Topics) == 3 && log.Topics[0] == cryptoTransferTopic && log.Topics[1] == "0x"+cryptoABIAddress(v.From) && log.Topics[2] == "0x"+cryptoABIAddress(v.To) {
			amount, e := cryptoHexInt(log.Data)
			if e != nil || amount.String() != v.Amount {
				return errCryptoSweepIntegrity
			}
			count++
		}
	}
	if r.Status == 1 && v.Token != "" && count != 1 {
		return errCryptoSweepIntegrity
	}
	return nil
}

type cryptoIntegrityJob struct {
	id, wallet, asset, destination, minimum, budget, state, operation, fingerprint string
	actor, chain, created, updated, leaseUntil                                     int64
	lease                                                                          string
	doc, config                                                                    []byte
}
type cryptoIntegrityItem struct {
	id, job, addressID, address, path, amount, gas, state string
	txs                                                   map[string]cryptoChainTransaction
}

// These authorization and finality checks also run on normal startup: a shallow
// wallet check must not allow corrupted persisted money jobs to resume.
func (s *Store) validateCryptoSweeps(_ bool) error {
	tx, e := s.cryptoWalletTx(context.Background())
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, query := range []string{
		"SELECT COUNT(*) FROM crypto_funding_addresses f LEFT JOIN crypto_wallets w ON w.id=f.wallet_id WHERE w.id IS NULL",
		"SELECT COUNT(*) FROM crypto_sweep_items i LEFT JOIN crypto_sweep_jobs j ON j.id=i.job_id WHERE j.id IS NULL",
		"SELECT COUNT(*) FROM crypto_chain_transactions t LEFT JOIN crypto_sweep_items i ON i.id=t.item_id WHERE i.id IS NULL OR i.job_id<>t.job_id OR t.nonce<0",
		"SELECT COUNT(*) FROM (SELECT chain_id,LOWER(from_address),nonce FROM crypto_chain_transactions GROUP BY chain_id,LOWER(from_address),nonce HAVING COUNT(*)<>1) duplicates",
	} {
		var n int
		if e = tx.QueryRow(query).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return errCryptoSweepIntegrity
		}
	}
	// v0.36 wallets legitimately have no funding row until first use. Existing
	// rows and every wallet used by a sweep must have the exact independent path.
	funding := map[string]cryptoFundingAddress{}
	rows, e := tx.Query("SELECT wallet_id,address,path FROM crypto_funding_addresses")
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		var f cryptoFundingAddress
		if e = rows.Scan(&id, &f.Address, &f.Path); e != nil {
			rows.Close()
			return e
		}
		funding[id] = f
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for id, f := range funding {
		wallet, e := cryptoWalletByID(tx, id)
		if e != nil {
			return e
		}
		want, e := cryptoFunding(wallet)
		if e != nil || wallet.Mode != "hot" || f != want {
			return errCryptoSweepIntegrity
		}
	}
	rows, e = tx.Query("SELECT id,actor_id,operation_id,fingerprint,wallet_id,chain_id,asset_id,destination,min_atoms,max_gas_atoms,state,doc,config,created,updated,lease_token,lease_until FROM crypto_sweep_jobs")
	if e != nil {
		return e
	}
	jobs := []cryptoIntegrityJob{}
	for rows.Next() {
		var j cryptoIntegrityJob
		if e = rows.Scan(&j.id, &j.actor, &j.operation, &j.fingerprint, &j.wallet, &j.chain, &j.asset, &j.destination, &j.minimum, &j.budget, &j.state, &j.doc, &j.config, &j.created, &j.updated, &j.lease, &j.leaseUntil); e != nil {
			rows.Close()
			return e
		}
		jobs = append(jobs, j)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, j := range jobs {
		if e = s.validateCryptoSweepJob(tx, j, funding[j.wallet]); e != nil {
			return fmt.Errorf("%w: job %s", e, j.id)
		}
	}
	return nil
}

func (s *Store) validateCryptoSweepJob(tx *persistence.Tx, j cryptoIntegrityJob, funding cryptoFundingAddress) error {
	var quote cryptoSweepQuote
	var preview CryptoSweepPreview
	if s.vault.open(j.config, &quote) != nil || json.Unmarshal(j.doc, &preview) != nil || !bytes.Equal(jsonBytes(preview), jsonBytes(quote.Preview)) || !quote.Preview.CanSubmit || quote.Preview.Quote != "" || len(quote.Preview.Items) == 0 || len(quote.Preview.Items) > 1000 {
		return errCryptoSweepIntegrity
	}
	p := quote.Preview
	if quote.ActorID != j.actor || j.fingerprint != digest(j.wallet+"|"+base64.StdEncoding.EncodeToString(j.config)) || !operationPattern.MatchString(j.operation) || p.WalletID != j.wallet || quote.Chain.ChainID != j.chain || p.ChainID != quote.Chain.ID || quote.Asset.ID != j.asset || p.AssetID != j.asset || quote.Asset.ChainID != j.chain || p.Destination != j.destination || p.MinAtoms != j.minimum || p.MaxGasAtoms != j.budget || funding.Address == "" || p.FundingAddress != funding.Address || cryptoSweepAllowed(quote.Chain) != nil || j.created <= 0 || j.updated < j.created || p.CheckedAt > j.created || p.ExpiresAt < j.created || p.ExpiresAt <= p.CheckedAt {
		return errCryptoSweepIntegrity
	}
	switch j.state {
	case "queued", "waiting", "complete", "failed", "review_required", "cancelled":
		if j.lease != "" || j.leaseUntil != 0 {
			return errCryptoSweepIntegrity
		}
	case "running":
		if j.lease == "" || j.leaseUntil <= 0 {
			return errCryptoSweepIntegrity
		}
	default:
		return errCryptoSweepIntegrity
	}
	plans := map[string]CryptoSweepPlanItem{}
	for _, plan := range p.Items {
		if _, duplicate := plans[plan.AddressID]; duplicate {
			return errCryptoSweepIntegrity
		}
		var wallet, address, path string
		if e := tx.QueryRow("SELECT wallet_id,address,path FROM crypto_wallet_addresses WHERE id=?", plan.AddressID).Scan(&wallet, &address, &path); e != nil {
			return e
		}
		if wallet != j.wallet || address != plan.Address || path != plan.Path {
			return errCryptoSweepIntegrity
		}
		plans[plan.AddressID] = plan
	}
	rows, e := tx.Query("SELECT id,job_id,address_id,address,path,amount_atoms,gas_needed_atoms,state FROM crypto_sweep_items WHERE job_id=?", j.id)
	if e != nil {
		return e
	}
	items := map[string]*cryptoIntegrityItem{}
	for rows.Next() {
		var i cryptoIntegrityItem
		if e = rows.Scan(&i.id, &i.job, &i.addressID, &i.address, &i.path, &i.amount, &i.gas, &i.state); e != nil {
			rows.Close()
			return e
		}
		plan, ok := plans[i.addressID]
		if !ok || plan.Address != i.address || plan.Path != i.path || plan.AmountAtoms != i.amount || plan.TopupAtoms != i.gas {
			rows.Close()
			return errCryptoSweepIntegrity
		}
		i.txs = map[string]cryptoChainTransaction{}
		items[i.id] = &i
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	if len(items) != len(plans) {
		return errCryptoSweepIntegrity
	}
	rows, e = tx.Query("SELECT "+cryptoTxColumns+",block_number,block_hash,receipt FROM crypto_chain_transactions WHERE job_id=?", j.id)
	if e != nil {
		return e
	}
	for rows.Next() {
		var v cryptoChainTransaction
		var block uint64
		var hash string
		var receipt []byte
		if e = rows.Scan(&v.ID, &v.JobID, &v.ItemID, &v.Kind, &v.ChainID, &v.From, &v.To, &v.Token, &v.Amount, &v.Nonce, &v.Hash, &v.Raw, &v.GasLimit, &v.GasPrice, &v.State, &block, &hash, &receipt); e != nil {
			rows.Close()
			return e
		}
		i := items[v.ItemID]
		if i == nil {
			rows.Close()
			return errCryptoSweepIntegrity
		}
		if _, e = cryptoValidateSignedIntent(v); e == nil {
			e = cryptoTransactionMatchesPlan(v, quote, plans[i.addressID])
		}
		if e == nil {
			e = s.validateCryptoSweepReceipt(v, block, hash, receipt)
		}
		if e != nil {
			rows.Close()
			return e
		}
		if _, exists := i.txs[v.Kind]; exists {
			rows.Close()
			return errCryptoSweepIntegrity
		}
		i.txs[v.Kind] = v
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	for _, i := range items {
		gas, sweep := i.txs["gas"], i.txs["sweep"]
		if sweep.ID != "" && gas.ID != "" && gas.State != "confirmed" {
			return errCryptoSweepIntegrity
		}
		switch i.state {
		case "queued":
			if len(i.txs) != 0 {
				return errCryptoSweepIntegrity
			}
		case "gas_pending":
			if gas.ID == "" || gas.State == "reverted" || sweep.ID != "" {
				return errCryptoSweepIntegrity
			}
		case "sweep_pending":
			if sweep.ID == "" || (sweep.State != "signed" && sweep.State != "pending") {
				return errCryptoSweepIntegrity
			}
		case "complete":
			if sweep.ID == "" || sweep.State != "confirmed" {
				return errCryptoSweepIntegrity
			}
		case "failed":
			if gas.State != "reverted" && sweep.State != "reverted" {
				return errCryptoSweepIntegrity
			}
		default:
			return errCryptoSweepIntegrity
		}
		if j.state == "complete" && i.state != "complete" || j.state == "queued" && i.state != "queued" {
			return errCryptoSweepIntegrity
		}
		if j.state == "cancelled" {
			for _, v := range i.txs {
				if v.State != "confirmed" && v.State != "reverted" {
					return errCryptoSweepIntegrity
				}
			}
		}
	}
	return nil
}
