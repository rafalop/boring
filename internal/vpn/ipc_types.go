package vpn

import "fmt"

type CmdKind int

const (
	Nop CmdKind = iota
	Up
	Down
	List
	Shutdown
)

var cmdKindNames = map[CmdKind]string{
	Nop:      "Nop",
	Up:       "Up",
	Down:     "Down",
	List:     "List",
	Shutdown: "Shutdown",
}

func (k CmdKind) String() string {
	n, ok := cmdKindNames[k]
	if !ok {
		return fmt.Sprintf("%d", int(k))
	}
	return n
}

// Cmd represents a command sent to the daemon
type Cmd struct {
	Kind CmdKind `json:"kind"`
	Vpn  *Desc   `json:"vpn,omitempty"`
}

// Info contains information about the daemon, e.g. the build commit
type Info struct {
	Commit string `json:"commit"`
}

// Resp represents a response from the daemon
type Resp struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Vpns    map[string]Desc `json:"vpns,omitempty"`
	Info    Info            `json:"info,omitempty"`
}
