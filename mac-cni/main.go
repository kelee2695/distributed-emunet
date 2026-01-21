// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package main

import (
	"MAC_CNI/cmd"
	"runtime"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	cmd.PluginMain()
}
