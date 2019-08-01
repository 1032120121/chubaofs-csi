/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"os"

	cfs "github.com/chubaofs/chubaofs-csi/pkg/chubaofs"
	"github.com/spf13/cobra"
)

var (
	endpoint   string
	nodeID     string
	driverName string
)

func init() {
	flag.Set("logtostderr", "true")
}

func main() {
	flag.CommandLine.Parse([]string{})

	cmd := &cobra.Command{
		Use:   "chubaofsplugin --endpoint <endpoint> --nodeid <nodeid>",
		Short: "CSI based chubaofs plugin driver",
		Run: func(cmd *cobra.Command, args []string) {
			handle()
		},
	}

	cmd.Flags().AddGoFlagSet(flag.CommandLine)

	cmd.PersistentFlags().StringVar(&nodeID, "nodeid", "", "node id")
	cmd.MarkPersistentFlagRequired("nodeid")

	cmd.PersistentFlags().StringVar(&endpoint, "endpoint", "", "CSI endpoint")
	cmd.MarkPersistentFlagRequired("endpoint")

	cmd.PersistentFlags().StringVar(&driverName, "drivername", "chubaofs.csi.k8s.io", "name of the driver")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(1)
	}

	os.Exit(0)
}

func handle() {
	d := cfs.NewDriver(driverName, nodeID, endpoint)
	d.Run()
}
