package bridge

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Keep replay tombstones outside frequently rewritten job state. Shards retain
// no prompts, output or original idempotency keys. Never expire them silently.
const receiptShardLimit = 16 << 20

func (a *App) receiptShard(key string) (string, map[string]Receipt, error) {
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return "", nil, errors.New("invalid receipt key")
	}
	root := filepath.Join(a.dir, "receipts")
	if err = PrepareDataDirectory(root); err != nil {
		return "", nil, err
	}
	path := filepath.Join(root, key[:2]+".json")
	data, err := ReadLocalFile(path, receiptShardLimit)
	if errors.Is(err, os.ErrNotExist) {
		return path, map[string]Receipt{}, nil
	}
	if err != nil {
		return "", nil, err
	}
	var entries map[string]Receipt
	if json.Unmarshal(data, &entries) != nil || entries == nil {
		return "", nil, errors.New("receipt index is corrupt; restore it before accepting requests")
	}
	for id, receipt := range entries {
		if !validReceipt(id, receipt) || id[:2] != key[:2] {
			return "", nil, errors.New("invalid receipt index; preserved for recovery")
		}
	}
	return path, entries, nil
}
func validReceipt(key string, receipt Receipt) bool {
	for _, value := range []string{key, receipt.Hash, receipt.JobID} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return false
		}
	}
	return true
}
func (a *App) receipt(key string) (Receipt, bool, error) {
	_, entries, err := a.receiptShard(key)
	if err != nil {
		return Receipt{}, false, err
	}
	r, ok := entries[key]
	return r, ok, nil
}
func (a *App) reserveReceipt(key string, receipt Receipt) error {
	if !validReceipt(key, receipt) {
		return errors.New("invalid receipt")
	}
	path, entries, err := a.receiptShard(key)
	if err != nil {
		return err
	}
	if previous, ok := entries[key]; ok && previous != receipt {
		return errors.New("receipt conflict")
	}
	entries[key] = receipt
	return saveReceiptShard(path, entries)
}
func saveReceiptShard(path string, entries map[string]Receipt) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if len(data) > receiptShardLimit {
		return fail(507, "receipt_storage_full", "Replay protection storage is full. Export your history and create a separate installation; do not delete receipt files.")
	}
	return writeLocalFile(path, data)
}
func (a *App) migrateReceipts() error {
	// A restart during migration is safe: reservations are idempotent, and the
	// old ledger is removed from state only after every shard is durable.
	shards := map[string]map[string]Receipt{}
	for key, receipt := range a.state.Receipts {
		if !validReceipt(key, receipt) {
			return errors.New("invalid legacy receipt index; preserved for recovery")
		}
		if shards[key[:2]] == nil {
			shards[key[:2]] = map[string]Receipt{}
		}
		shards[key[:2]][key] = receipt
	}
	for _, migrated := range shards {
		var sample string
		for key := range migrated {
			sample = key
			break
		}
		path, entries, err := a.receiptShard(sample)
		if err != nil {
			return err
		}
		for key, receipt := range migrated {
			if previous, ok := entries[key]; ok && previous != receipt {
				return errors.New("legacy receipt conflict; preserved for recovery")
			}
			entries[key] = receipt
		}
		if err := saveReceiptShard(path, entries); err != nil {
			return err
		}
	}
	a.state.Receipts = nil
	return nil
}
