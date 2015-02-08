// Implementation of a MultiEchoServer. Students should write their code in this file.

package p0

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

type client struct {
	id      int
	conn    *net.TCPConn
	inMsg   chan string
	hubMsg  chan string
	disc    chan<- int
	cleanup chan bool
}

type multiEchoServer struct {
	clients  []client
	listener *net.TCPListener
	cleanup  chan bool
}

// New creates and returns (but does not start) a new MultiEchoServer.
func New() MultiEchoServer {
	return &multiEchoServer{cleanup: make(chan bool)}
}

func Hub(mes *multiEchoServer) {

	// Make a channel every client writes to in order to communicate back to
	// the Hub
	clientChan := make(chan string)

	// Tell Hub to add client to server list
	newClientChan := make(chan client)

	// Tell Hub to remove client from server list
	doneClientChan := make(chan int)

	// Tell Listener thread to cleanup
	listenerCleanup := make(chan bool)

	// This goroutine is responsible for accepting new connections and creating
	// a client to handle each one
	go func(mes *multiEchoServer) {
		clientId := 0
		for {
			// Wait for a connection for this new client object
			select {
			case <-listenerCleanup:
				err := mes.listener.Close()
				if err != nil {
					log.Fatal(err)
				} else {
					log.Println("Closed listener")
				}
				return
			default:
				mes.listener.SetDeadline(time.Now().Add(time.Second * 5))
				newConn, err := mes.listener.AcceptTCP()
				if err != nil {
					if opErr, ok := err.(*net.OpError); ok && !opErr.Timeout() {
						log.Fatal(err)
					} else if opErr.Timeout() {
						continue
					}
				}

				// Create new client
				c := client{id: clientId, inMsg: make(chan string, 100),
					hubMsg: clientChan, disc: doneClientChan, conn: newConn}
				clientId++

				// Add client to server list
				newClientChan <- c

				// Kick off client
				go c.Start()
			}
		}
	}(mes)

	for {
		select {
		case <-mes.cleanup:
			for _, c := range mes.clients {
				c.cleanup <- true
			}

			listenerCleanup <- true

			return
		case broadcast := <-clientChan:
			for _, c := range mes.clients {
				select {
				case c.inMsg <- broadcast:
				default:
					log.Println("Dropping msg for client ", c.id)
				}
			}
			//log.Println("Got broadcast: ", broadcast)
		case client := <-newClientChan:
			// Add new client to the server list
			mes.clients = append(mes.clients, client)
			log.Println("Got new client")
		case client := <-doneClientChan:
			err := mes.RemoveClient(client)
			if err != nil {
				log.Fatal(err)
			}
			log.Println("Removing client ", client)
		}
	}
	return
}

func (mes *multiEchoServer) RemoveClient(id int) error {
	found := false
	// Remove closed client from the server list
	for idx, foundClient := range mes.clients {
		// Found client matching the id passed in
		if foundClient.id == id {
			// Rebuild the client list without that client
			mes.clients = append(mes.clients[:idx],
				mes.clients[idx+1:]...)

			found = true
			break
		}
	}

	if found == false {
		return fmt.Errorf("Unable to find client %d", id)
	}

	return nil
}

func (mes *multiEchoServer) Start(port int) error {
	var err error
	service := "0.0.0.0:" + strconv.Itoa(port)
	tcpAddr, err := net.ResolveTCPAddr("tcp", service)

	mes.listener, err = net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		log.Fatal(err)
		return err
	}

	log.Println("Starting server")

	// Kick off the main hub thread
	go Hub(mes)

	return nil
}

func (mes *multiEchoServer) Close() {
	mes.cleanup <- true
}

func (mes *multiEchoServer) Count() int {
	return len(mes.clients)
}

func (c *client) Start() {
	defer c.conn.Close()

	done := make(chan bool)
	cleanup := false

	// Go routine listens for new data and passes it on to the server's Hub
	// channel
	go func(hubMsg chan<- string) {
		connReader := bufio.NewReaderSize(c.conn, 768)
		for {
			if cleanup {
				return
			}
			buf, err := connReader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					log.Println("read error:", err)
				} else {
					log.Println("Got EOF, closing")
					break
				}
			}

			//log.Println("Got msg: ", string(buf))
			hubMsg <- string(buf)
		}

		done <- true
	}(c.hubMsg)

	// Rest of this function listens for outgoing data on it's outgoing channel
	// and writes it back to the connection
	connWriter := bufio.NewWriterSize(c.conn, 768)
	for {
		select {
		case buf := <-c.inMsg:
			//log.Println("Sending msg: ", buf)
			_, err := connWriter.Write([]byte(buf))
			if err != nil {
				if err != io.EOF {
					log.Println("write error:", err)
				}
				break
			}

			err = connWriter.Flush()
			if err != nil {
				log.Println("flush error:", err)
			}

		// If the reader for this client calls it quits, then we're done too
		// since we can't write to a closed channel
		case <-done:
			c.disc <- c.id
			return

		case <-c.cleanup:
			cleanup = true
		}
	}

}
