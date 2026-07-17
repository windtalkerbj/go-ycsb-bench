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
	"fmt"

	"github.com/magiconair/properties"
	"github.com/pingcap/go-ycsb/pkg/ycsb"
	"github.com/tikv/client-go/v2/config"
)

const (
	tikvPD = "tikv.pd"
	// raw, txn, or coprocessor
	tikvType       = "tikv.type"
	tikvConnCount  = "tikv.conncount"
	tikvBatchSize  = "tikv.batchsize"
	tikvAPIVersion = "tikv.apiversion"
	// update 模式：getput（默认，Get+Put 两次 RPC）| cas（Read 缓存 + CompareAndSwap，RMW 省一次 RPC）
	tikvUpdateMode = "tikv.updatemode"
	// TLS options
	tikvClusterSSLCA   = "tikv.cluster-ssl-ca"
	tikvClusterSSLCert = "tikv.cluster-ssl-cert"
	tikvClusterSSLKey  = "tikv.cluster-ssl-key"
)

type tikvCreator struct {
}

func (c tikvCreator) Create(p *properties.Properties) (ycsb.DB, error) {
	config.UpdateGlobal(func(c *config.Config) {
		c.TiKVClient.GrpcConnectionCount = p.GetUint(tikvConnCount, 128)
		c.TiKVClient.MaxBatchSize = p.GetUint(tikvBatchSize, 128)
		if ca := p.GetString(tikvClusterSSLCA, ""); ca != "" {
			c.Security.ClusterSSLCA = ca
		}
		if cert := p.GetString(tikvClusterSSLCert, ""); cert != "" {
			c.Security.ClusterSSLCert = cert
		}
		if key := p.GetString(tikvClusterSSLKey, ""); key != "" {
			c.Security.ClusterSSLKey = key
		}
	})

	tp := p.GetString(tikvType, "raw")
	switch tp {
	case "raw":
		return createRawDB(p)
	case "txn":
		return createTxnDB(p)
	default:
		return nil, fmt.Errorf("unsupported type %s", tp)
	}
}

func init() {
	ycsb.RegisterDBCreator("tikv", tikvCreator{})
}
