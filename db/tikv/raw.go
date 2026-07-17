// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package tikv

import (
	"context"
	"fmt"
	"strings"

	"github.com/magiconair/properties"
	"github.com/pingcap/errors"
	"github.com/pingcap/go-ycsb/pkg/util"
	"github.com/pingcap/go-ycsb/pkg/ycsb"
	"github.com/pingcap/kvproto/pkg/kvrpcpb"
	"github.com/tikv/client-go/v2/rawkv"
	"github.com/tikv/client-go/v2/config"
)

type rawDB struct {
	db      *rawkv.Client
	r       *util.RowCodec
	bufPool *util.BufPool
	casMode bool
}

type contextKey string

const stateKey = contextKey("tikvRawDB")

// rawDBState 记录该线程最近一次 Read 的原始行，供 CAS 模式的 Update 做 previousValue
type rawDBState struct {
	key string // getRowKey(table, key) 的结果
	row []byte // Read 返回的原始编码行
}

func createRawDB(p *properties.Properties) (ycsb.DB, error) {
	pdAddr := p.GetString(tikvPD, "127.0.0.1:2379")
	apiVersionStr := strings.ToUpper(p.GetString(tikvAPIVersion, "V1"))
	apiVersion, ok := kvrpcpb.APIVersion_value[apiVersionStr]
	if !ok {
		return nil, errors.Errorf("Invalid tikv apiversion %s.", apiVersionStr)
	}
	db, err := rawkv.NewClientWithOpts(context.Background(), strings.Split(pdAddr, ","),
		rawkv.WithAPIVersion(kvrpcpb.APIVersion(apiVersion)),
		rawkv.WithSecurity(config.GetGlobalConfig().Security))
	if err != nil {
		return nil, err
	}

	casMode := strings.ToLower(p.GetString(tikvUpdateMode, "getput")) == "cas"
	if casMode {
		// 注意：atomic 模式下所有写请求都带 ForCas 标记（单行事务路径），
		// 与非 atomic 写入混用会破坏线性一致性，仅用于独立压测环境
		db.SetAtomicForCAS(true)
	}

	bufPool := util.NewBufPool()

	return &rawDB{
		db:      db,
		r:       util.NewRowCodec(p),
		bufPool: bufPool,
		casMode: casMode,
	}, nil
}

func (db *rawDB) Close() error {
	return db.db.Close()
}

func (db *rawDB) InitThread(ctx context.Context, _ int, _ int) context.Context {
	return context.WithValue(ctx, stateKey, &rawDBState{})
}

func (db *rawDB) CleanupThread(ctx context.Context) {
}

func (db *rawDB) getRowKey(table string, key string) []byte {
	return util.Slice(fmt.Sprintf("%s:%s", table, key))
}

func (db *rawDB) Read(ctx context.Context, table string, key string, fields []string) (map[string][]byte, error) {
	rowKey := db.getRowKey(table, key)
	row, err := db.db.Get(ctx, rowKey)
	if err != nil {
		return nil, err
	} else if row == nil {
		return nil, nil
	}

	if db.casMode {
		if state, ok := ctx.Value(stateKey).(*rawDBState); ok {
			state.key = string(rowKey)
			state.row = row
		}
	}

	return db.r.Decode(row, fields)
}

func (db *rawDB) BatchRead(ctx context.Context, table string, keys []string, fields []string) ([]map[string][]byte, error) {
	rowKeys := make([][]byte, len(keys))
	for i, key := range keys {
		rowKeys[i] = db.getRowKey(table, key)
	}
	values, err := db.db.BatchGet(ctx, rowKeys)
	if err != nil {
		return nil, err
	}
	rowValues := make([]map[string][]byte, len(keys))

	for i, value := range values {
		if len(value) > 0 {
			rowValues[i], err = db.r.Decode(value, fields)
		} else {
			rowValues[i] = nil
		}
	}
	return rowValues, nil
}

func (db *rawDB) Scan(ctx context.Context, table string, startKey string, count int, fields []string) ([]map[string][]byte, error) {
	_, rows, err := db.db.Scan(ctx, db.getRowKey(table, startKey), nil, count)
	if err != nil {
		return nil, err
	}

	res := make([]map[string][]byte, len(rows))
	for i, row := range rows {
		if row == nil {
			res[i] = nil
			continue
		}

		v, err := db.r.Decode(row, fields)
		if err != nil {
			return nil, err
		}
		res[i] = v
	}

	return res, nil
}

func (db *rawDB) Update(ctx context.Context, table string, key string, values map[string][]byte) error {
	rowKey := db.getRowKey(table, key)

	// CAS 模式：若本次 Update 命中同线程 Read 缓存的行，直接 CompareAndSwap，
	// 省掉一次 Get（workloadf 的 RMW 从 3 次 RPC 降为 2 次）；冲突则回退经典 Get+Put
	if db.casMode {
		if state, ok := ctx.Value(stateKey).(*rawDBState); ok && state.row != nil && state.key == string(rowKey) {
			prevRow := state.row
			state.key = ""
			state.row = nil

			data, err := db.r.Decode(prevRow, nil)
			if err != nil {
				return err
			}
			for field, value := range values {
				data[field] = value
			}

			buf := db.bufPool.Get()
			defer func() {
				db.bufPool.Put(buf)
			}()
			buf, err = db.r.Encode(buf, data)
			if err != nil {
				return err
			}

			_, success, err := db.db.CompareAndSwap(ctx, rowKey, prevRow, buf)
			if err != nil {
				return err
			}
			if success {
				return nil
			}
		}
	}

	row, err := db.db.Get(ctx, rowKey)
	if err != nil {
		return err
	}

	data, err := db.r.Decode(row, nil)
	if err != nil {
		return err
	}

	for field, value := range values {
		data[field] = value
	}

	// Update data and use Insert to overwrite.
	return db.Insert(ctx, table, key, data)
}

func (db *rawDB) BatchUpdate(ctx context.Context, table string, keys []string, values []map[string][]byte) error {
	var rawKeys [][]byte
	var rawValues [][]byte
	for i, key := range keys {
		// TODO should we check the key exist?
		rawKeys = append(rawKeys, db.getRowKey(table, key))
		rawData, err := db.r.Encode(nil, values[i])
		if err != nil {
			return err
		}
		rawValues = append(rawValues, rawData)
	}
	return db.db.BatchPut(ctx, rawKeys, rawValues)
}

func (db *rawDB) Insert(ctx context.Context, table string, key string, values map[string][]byte) error {
	// Simulate TiDB data
	buf := db.bufPool.Get()
	defer func() {
		db.bufPool.Put(buf)
	}()

	buf, err := db.r.Encode(buf, values)
	if err != nil {
		return err
	}

	return db.db.Put(ctx, db.getRowKey(table, key), buf)
}

func (db *rawDB) BatchInsert(ctx context.Context, table string, keys []string, values []map[string][]byte) error {
	var rawKeys [][]byte
	var rawValues [][]byte
	for i, key := range keys {
		rawKeys = append(rawKeys, db.getRowKey(table, key))
		rawData, err := db.r.Encode(nil, values[i])
		if err != nil {
			return err
		}
		rawValues = append(rawValues, rawData)
	}
	return db.db.BatchPut(ctx, rawKeys, rawValues)
}

func (db *rawDB) Delete(ctx context.Context, table string, key string) error {
	return db.db.Delete(ctx, db.getRowKey(table, key))
}

func (db *rawDB) BatchDelete(ctx context.Context, table string, keys []string) error {
	rowKeys := make([][]byte, len(keys))
	for i, key := range keys {
		rowKeys[i] = db.getRowKey(table, key)
	}
	return db.db.BatchDelete(ctx, rowKeys)
}

func (db *rawDB) Count(ctx context.Context, table string) (int64, error) {
	// For TiKV raw mode, we need to scan all keys with the table prefix
	startKey := db.getRowKey(table, "")
	endKey := append(startKey, 0xFF, 0xFF, 0xFF, 0xFF) // Create an end boundary

	var count int64
	const batchSize = 10000

	for {
		keys, _, err := db.db.Scan(ctx, startKey, endKey, batchSize)
		if err != nil {
			return 0, err
		}

		if len(keys) == 0 {
			break
		}

		count += int64(len(keys))

		// Move to next batch
		if len(keys) < batchSize {
			// Last batch
			break
		}

		// Use last key as new start key for next iteration
		startKey = append(keys[len(keys)-1], 0x00)
	}

	return count, nil
}

func (db *rawDB) Clean(ctx context.Context, table string) error {
	// For TiKV raw mode, we use DeleteRange to efficiently delete all keys with the table prefix
	startKey := db.getRowKey(table, "")
	endKey := append(startKey, 0xFF, 0xFF, 0xFF, 0xFF) // Create an end boundary

	return db.db.DeleteRange(ctx, startKey, endKey)
}
