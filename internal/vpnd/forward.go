package vpnd

import (
	"io"
	"net"
)

// splice shovels bytes between two connections until either side closes.
func splice(c1, c2 net.Conn) {
	defer c1.Close()
	defer c2.Close()
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(c1, c2)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(c2, c1)
		done <- struct{}{}
	}()

	<-done
}
