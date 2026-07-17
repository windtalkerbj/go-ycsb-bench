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

package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/pingcap/go-ycsb/pkg/client"
	"github.com/pingcap/go-ycsb/pkg/measurement"
	"github.com/pingcap/go-ycsb/pkg/prop"
	"github.com/spf13/cobra"
)

func runClientCommandFunc(cmd *cobra.Command, args []string, doTransactions bool, command string) {
	var dbName string
	if len(args) > 0 {
		dbName = args[0]
	}

	// Validate prometheus parameters
	if cmd.Flags().Changed("prome-server") && !cmd.Flags().Changed("bench-no") {
		fmt.Println("ERROR: --bench-no is required when using --prome-server")
		return
	}

	// Validate statistic parameters
	if statistic {
		if !cmd.Flags().Changed("prome-server") {
			fmt.Println("ERROR: --prome-server is required when using --statistic")
			return
		}
		if !cmd.Flags().Changed("bench-no") {
			fmt.Println("ERROR: --bench-no is required when using --statistic")
			return
		}
	}

	// Statistic mode: skip benchmark entirely, query Prometheus directly
	if statistic {
		fmt.Println("**********************************************")
		fmt.Println("Statistic mode: Querying metrics from Prometheus...")
		fmt.Printf("Prometheus URL: %s\n", promeServer)
		fmt.Printf("Benchmark ID: %s\n", benchNo)

		csvFile, err := measurement.QueryOnly(promeServer, benchNo)
		if err != nil {
			fmt.Printf("Failed to query and aggregate metrics: %v\n", err)
		} else {
			fmt.Printf("Successfully saved statistics to: %s\n", csvFile)
		}
		fmt.Println("**********************************************")
		return
	}

	if dbName == "" {
		fmt.Println("ERROR: db name is required")
		return
	}

	initialGlobal(dbName, func() {
		doTransFlag := "true"
		if !doTransactions {
			doTransFlag = "false"
		}
		globalProps.Set(prop.DoTransactions, doTransFlag)
		globalProps.Set(prop.Command, command)

		if cmd.Flags().Changed("threads") {
			// We set the threadArg via command line.
			globalProps.Set(prop.ThreadCount, strconv.Itoa(threadsArg))
		}

		if cmd.Flags().Changed("target") {
			globalProps.Set(prop.Target, strconv.Itoa(targetArg))
		}

		if cmd.Flags().Changed("interval") {
			globalProps.Set(prop.LogInterval, strconv.Itoa(reportInterval))
		}
	})

	// For tikv raw mode, apiversion defaults to V1 if not explicitly specified
	tikvType := globalProps.GetString("tikv.type", "raw")
	if dbName == "tikv" && tikvType == "raw" {
		if _, ok := globalProps.Get("tikv.apiversion"); !ok {
			globalProps.Set("tikv.apiversion", "V1")
		}
	}

	// Prometheus features are only supported for 'run tikv' with tikv.type="raw"
	isPrometheusSupported := dbName == "tikv" && tikvType == "raw" && doTransactions

	if cmd.Flags().Changed("prome-server") && !isPrometheusSupported {
		fmt.Printf("ERROR: --prome-server is only supported for 'run tikv' with tikv.type=raw (current: db=%s, tikv.type=%s, command=%s)\n", dbName, tikvType, command)
		return
	}

	if isPrometheusSupported && cmd.Flags().Changed("prome-server") {
		measurement.SetPrometheusConfig(promeServer, benchNo)
	}

	fmt.Println("***************** properties *****************")
	for key, value := range globalProps.Map() {
		fmt.Printf("\"%s\"=\"%s\"\n", key, value)
	}
	fmt.Println("**********************************************")

	// Print the complete command for debugging
	fmt.Println("***************** command *****************")
	fmt.Printf("./bin/go-ycsb %s %s", cmd.Name(), dbName)
	if cmd.Flags().Changed("threads") {
		fmt.Printf(" --threads %d", threadsArg)
	}
	if cmd.Flags().Changed("prome-server") {
		fmt.Printf(" --prome-server %s", promeServer)
	}
	if cmd.Flags().Changed("bench-no") {
		fmt.Printf(" --bench-no %s", benchNo)
	}
	fmt.Println()
	fmt.Println("**********************************************")

	c := client.NewClient(globalProps, globalWorkload, globalDB)
	start := time.Now()
	c.Run(globalContext)
	fmt.Println("**********************************************")
	fmt.Printf("Run finished, takes %s\n", time.Now().Sub(start))
	measurement.Output()
}

func runLoadCommandFunc(cmd *cobra.Command, args []string) {
	runClientCommandFunc(cmd, args, false, "load")
}

func runTransCommandFunc(cmd *cobra.Command, args []string) {
	runClientCommandFunc(cmd, args, true, "run")
}

var (
	threadsArg     int
	targetArg      int
	reportInterval int
	promeServer    string
	benchNo        string
	statistic      bool
)

func initClientCommand(m *cobra.Command) {
	m.Flags().StringSliceVarP(&propertyFiles, "property_file", "P", nil, "Spefify a property file")
	m.Flags().StringArrayVarP(&propertyValues, "prop", "p", nil, "Specify a property value with name=value")
	m.Flags().StringVar(&tableName, "table", "", "Use the table name instead of the default \""+prop.TableNameDefault+"\"")
	m.Flags().IntVar(&threadsArg, "threads", 1, "Execute using n threads - can also be specified as the \"threadcount\" property")
	m.Flags().IntVar(&targetArg, "target", 0, "Attempt to do n operations per second (default: unlimited) - can also be specified as the \"target\" property")
	m.Flags().IntVar(&reportInterval, "interval", 10, "Interval of outputting measurements in seconds")
	m.Flags().StringVar(&promeServer, "prome-server", "", "Prometheus URL for Remote Write (e.g., http://localhost:9090, requires --enable-feature=remote-write-receiver)")
	m.Flags().StringVar(&benchNo, "bench-no", "", "Benchmark number/ID for Prometheus metrics (required when using --prome-server)")
	m.Flags().BoolVar(&statistic, "statistic", false, "Query and aggregate metrics from Prometheus after test (requires --prome-server and --bench-no)")
}

func newLoadCommand() *cobra.Command {
	m := &cobra.Command{
		Use:   "load db",
		Short: "YCSB load benchmark",
		Args: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("statistic") && len(args) < 1 {
				return fmt.Errorf("requires at least 1 arg(s), only received 0")
			}
			return nil
		},
		Run: runLoadCommandFunc,
	}

	initClientCommand(m)
	return m
}

func newRunCommand() *cobra.Command {
	m := &cobra.Command{
		Use:   "run db",
		Short: "YCSB run benchmark",
		Args: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("statistic") && len(args) < 1 {
				return fmt.Errorf("requires at least 1 arg(s), only received 0")
			}
			return nil
		},
		Run: runTransCommandFunc,
	}

	initClientCommand(m)
	return m
}
