package cniservice

// Network control service has four methods: cmdAdd, cmdCheck, cmdDel
import (
	"github.com/containernetworking/cni/pkg/skel"
)

type CNIPlugin interface {
	Add(args *skel.CmdArgs) error
	Check(args *skel.CmdArgs) error
	Del(args *skel.CmdArgs) error
}