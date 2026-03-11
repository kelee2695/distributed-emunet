// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package main

import (
	"runtime"

	"github.com/emunet/emunet-operator/debug-cni/cmd"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	cmd.PluginMain()
}
