package feedproxy

import (
	"net"
	"sync"
	"time"
)

const (
	// limits connection attempts to this many per source IP
	maxIncomingConnectionRequestsPerSrcIP = 3

	// reject more than maxIncomingConnectionRequestsPerSrcIP over this many seconds
	maxIncomingConnectionRequestSeconds = 10
)

// structs to hold incoming connection data
type incomingConnectionTracker struct {
	mu               sync.RWMutex
	connections      []incomingConnection
	connectionNumber uint // to allocate connection numbers
}

func (t *incomingConnectionTracker) getNum() (num uint) {
	// return a non-duplicate connection number

	var dupe bool

	t.mu.Lock()
	defer t.mu.Unlock()

	for {

		// determine next available connection number
		t.connectionNumber++
		if t.connectionNumber == 0 { // don't have a connection with number 0
			t.connectionNumber++
		}

		// is the connection number already in use
		dupe = false
		for _, c := range t.connections {
			if t.connectionNumber == c.connNum {
				dupe = true
				break
			}
		}

		// if not a duplicate, break out of loop
		if !dupe {
			break
		}
	}

	return t.connectionNumber
}

func (t *incomingConnectionTracker) evict() {
	// evicts connections from the tracker if older than 10 seconds

	t.mu.Lock()
	defer t.mu.Unlock()

	// slice magic: https://stackoverflow.com/questions/20545743/how-to-remove-items-from-a-slice-while-ranging-over-it
	i := 0
	for _, c := range t.connections {

		// keep connections tracked if less than maxIncomingConnectionRequestSeconds seconds old
		if !c.connTime.Add(time.Second * maxIncomingConnectionRequestSeconds).Before(time.Now()) {
			t.connections[i] = c
			i++
		}
	}
	t.connections = t.connections[:i]

}

func (t *incomingConnectionTracker) check(srcIP net.IP, connNum uint) error {
	// checks an incoming connection
	// allows 'maxIncomingConnectionRequestsPerProto' connections every 10 seconds

	var connCount uint

	// count number of connections from this source IP
	t.mu.RLock()
	for _, c := range t.connections {
		if c.srcIP.Equal(srcIP) {
			connCount++
		}
	}
	t.mu.RUnlock()

	if connCount >= maxIncomingConnectionRequestsPerSrcIP {
		// if connecting too frequently, raise an error
		return ErrConnectingTooFrequently

	} else {
		// otherwise, don't raise an error but add this connection to the tracker
		t.mu.Lock()
		t.connections = append(t.connections, incomingConnection{
			srcIP:    srcIP,
			connTime: time.Now(),
			connNum:  connNum,
		})
		t.mu.Unlock()
	}

	return nil
}

// used to limit the number of connections over time from a single source IP
type incomingConnection struct {
	srcIP    net.IP
	connTime time.Time
	connNum  uint
}

func GetConnectionNumber() (num uint, err error) {
	// return connection number for new connection
	if !isInitialised() {
		return 0, ErrNotInitialised
	}
	return incomingConnTracker.getNum(), nil
}
