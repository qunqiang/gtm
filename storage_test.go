package gtm_test

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/quanhengzhuang/gtm"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type LevelStorage struct {
	db *leveldb.DB
}

func init() {
	var _ gtm.Storage = &LevelStorage{}
}

func NewLevelStorage() *LevelStorage {
	db, err := leveldb.OpenFile("level_db_storage", nil)
	if err != nil {
		log.Fatalf("open failed: %v", err)
	}

	return &LevelStorage{db}
}

func (s *LevelStorage) getRetryKey(retryTime time.Time, id string) []byte {
	return []byte(fmt.Sprintf("gtm-retry-%v-%v", retryTime.Unix(), id))
}

func (s *LevelStorage) Register(value interface{}) {
	gob.Register(value)
}

func (s *LevelStorage) SaveTransaction(g *gtm.Transaction) (id string, err error) {
	g.ID = time.Now().Format("20060102150405")

	// add retry key
	retry := s.getRetryKey(g.RetryAt, g.ID)
	if err := s.db.Put([]byte(retry), []byte(fmt.Sprintf("%v", g.ID)), nil); err != nil {
		return "", fmt.Errorf("db put failed: %v", err)
	}
	log.Printf("[storage] put retry key: %s", retry)

	// transaction
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(g); err != nil {
		return "", fmt.Errorf("gob encode err: %v", err)
	}

	key := fmt.Sprintf("gtm-transaction-%v", g.ID)
	if err := s.db.Put([]byte(key), buffer.Bytes(), nil); err != nil {
		return "", fmt.Errorf("db put failed: %v", err)
	}

	return g.ID, nil
}

func (s *LevelStorage) SaveTransactionResult(tx *gtm.Transaction, cost time.Duration, result gtm.Result) error {
	key := fmt.Sprintf("gtm-result-%v", tx.ID)
	if err := s.db.Put([]byte(key), []byte(result), nil); err != nil {
		return fmt.Errorf("db put failed: %v", err)
	}

	// delete retry
	if result == gtm.Success || result == gtm.Fail {
		key := s.getRetryKey(tx.RetryAt, tx.ID)
		if err := s.db.Delete(key, nil); err != nil {
			return fmt.Errorf("delete retry err: %v", err)
		}
		log.Printf("[storage] delete retry key: %s", key)
	}

	return nil
}

func (s *LevelStorage) SavePartnerResult(tx *gtm.Transaction, phase string, offset int, cost time.Duration, result gtm.Result) error {
	log.Printf("[storage] save partner result. id:%v, phase:%v, offset:%v, result:%v", tx.ID, phase, offset, result)

	key := fmt.Sprintf("gtm-partner-%v-%v-%v", tx.ID, phase, offset)
	if err := s.db.Put([]byte(key), []byte(result), nil); err != nil {
		return fmt.Errorf("db put failed: %v", err)
	}

	return nil
}

func (s *LevelStorage) UpdateTransactionRetryTime(g *gtm.Transaction, times int, newRetryTime time.Time) error {
	// add new retry key
	key := s.getRetryKey(newRetryTime, g.ID)
	value := []byte(fmt.Sprintf("%v", g.ID))
	if err := s.db.Put(key, value, nil); err != nil {
		return fmt.Errorf("put err: %v", err)
	}

	// delete old retry key
	oldKey := s.getRetryKey(g.RetryAt, g.ID)
	if err := s.db.Delete(oldKey, nil); err != nil {
		return fmt.Errorf("delete err: %v", err)
	}
	log.Printf("[storage] set retry key. new:%s, old:%s", key, oldKey)

	return nil
}

func (s *LevelStorage) GetTimeoutTransactions(count int) (transactions []*gtm.Transaction, err error) {
	var ids [][]byte

	// get retry ids
	iterator := s.db.NewIterator(util.BytesPrefix([]byte("gtm-retry-")), nil)
	for count > 0 && iterator.Next() {
		key := bytes.Split(iterator.Key(), []byte("-"))
		if len(key) < 4 {
			continue
		}

		timeUnix, _ := strconv.Atoi(string(key[2]))
		if int64(timeUnix) > time.Now().Unix() {
			break
		}

		ids = append(ids, key[3])
		count--
	}

	iterator.Release()

	// get transactions
	for _, id := range ids {
		value, err := s.db.Get([]byte(fmt.Sprintf("gtm-transaction-%s", id)), nil)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				log.Printf("tx not found: %s", id)
				continue
			}
			return nil, fmt.Errorf("get transaction err: %v", err)
		}

		var tx gtm.Transaction
		if err := gob.NewDecoder(bytes.NewReader(value)).Decode(&tx); err != nil {
			return nil, fmt.Errorf("gob decode err: %v", err)
		}

		transactions = append(transactions, &tx)
	}

	return transactions, nil
}

func (s *LevelStorage) GetPartnerResult(tx *gtm.Transaction, phase string, offset int) (gtm.Result, error) {
	key := fmt.Sprintf("gtm-partner-%v-%v-%v", tx.ID, phase, offset)
	if value, err := s.db.Get([]byte(key), nil); err != nil {
		return "", fmt.Errorf("db put failed: %v", err)
	} else {
		return gtm.Result(value), nil
	}
}
