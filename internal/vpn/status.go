package vpn

type Status int

const (
	Closed Status = iota
	Open
	Reconn
)
