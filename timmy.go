package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
)

type Mitmer struct {
	Conf    Config
	InConn  net.Conn
	OutConn net.Conn
}

func (m *Mitmer) GetDest() *net.TCPAddr {
	localAddr := m.InConn.LocalAddr()
	fmt.Printf("Local Address: %+v\n", m.InConn.LocalAddr())
	s := strings.Split(localAddr.String(), ":")
	p, err := strconv.Atoi(s[1])
	if err != nil {
		fmt.Printf("GetDest: error converting port to integer")
		// return invalid address / error here?
	}
	addr := net.TCPAddr{
		net.ParseIP(s[0]),
		p,
		"",
	}

	fmt.Printf("New Addr: %+v\n", addr)

	return &addr
}

var tlsConfig *tls.Config = &tls.Config{InsecureSkipVerify: true}

func (m *Mitmer) MitmConn() {
	dest := m.GetDest()
	var finalDest *net.TCPAddr
	var err error

	if _, ok := m.Conf.Ports[dest.Port]; ok {
		finalDest, err = net.ResolveTCPAddr("tcp4", m.Conf.Ports[dest.Port])
		if err != nil {
			fmt.Println("err: ", err)
		}
		fmt.Printf("Resolving address as: %+v\n", finalDest)
	} else if runtime.GOOS == "linux" {
		finalDest, err = GetOriginalDST(m.InConn.(*net.TCPConn))
		if err != nil {
			fmt.Println("get orig addr err: ", err)
			return
		}
	} else {
		// Bail out. Don't know what to do with the connection.
		// This can happen if someone connects to the transparent MiTM port
		// on a non-linux system
		m.InConn.Close()
		return
	}

	var outc net.Conn
	if m.Conf.TLSPorts.has(dest.Port) {
		// Upgrade server socket to SSL here

		fmt.Printf("Connecting using TLS: %+v\n", m.Conf.Ports[dest.Port])
		outc, err = tls.Dial("tcp", m.Conf.Ports[dest.Port], tlsConfig)

	} else {
		outc, err = net.Dial("tcp", m.Conf.Ports[dest.Port])
		m.OutConn = outc
	}

	if err != nil {
		fmt.Println("err connecting to orig dst: ", err)
		return
	}

	// Clean up
	defer m.InConn.Close()
	defer outc.Close()

	// Setup the server->victim data pump
	go func() {
		for {
			b := make([]byte, 1024)
			n, err := outc.Read(b)
			if err != nil {
				fmt.Println("err reading victim dest: ", err)
				break
			}
			n, err = m.InConn.Write(b[:n])
			//fmt.Printf("Writing back to victim: %+v\n", b[:n])
			if err != nil {
				fmt.Println("err writing back to victim: ", err)
				break
			}

		}
	}()

	// Set up the victim->server data pump
	for {
		b := make([]byte, 1024)
		n, err := m.InConn.Read(b)
		//fmt.Printf("Read bytes[%d] from [%+v]\n", n, origAddr)

		if err != nil {
			fmt.Println("err reading victim: ", err)
			break
		}
		n, err = outc.Write(b[:n])
		//fmt.Printf("Writing: %+v\n", b[:n])
		if err != nil {
			fmt.Println("err writing victim dest: ", err)
			break
		}

	}

	fmt.Printf("orig: %+v\n", finalDest)
}

func listener(l *net.TCPListener, c chan net.Conn) {
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("accept err: ", err)
			return
		}
		c <- conn
	}
}

func connMitmer(c chan net.Conn, conf Config) {
	for {
		conn := <-c
		m := Mitmer{
			InConn: conn.(*net.TCPConn),
			Conf:   conf,
		}
		go m.MitmConn()
	}
}

func main() {
	fmt.Println("Timmy starting up")

	conf, err := parseFlags()
	if err != nil {
		fmt.Println("err: ", err)
		return
	}
	fmt.Printf("Config: %+v\n", conf)

	listeners := make([]*net.TCPListener, 0)

	inC := make(chan net.Conn)

	for port := range conf.Ports {
		fmt.Println(port)
		l, err := net.ListenTCP("tcp", &net.TCPAddr{Port: port})
		if err != nil {
			fmt.Println("listen err: ", err)
			return
		}
		listeners = append(listeners, l)
	}

	for _, l := range listeners {
		go listener(l, inC)
	}
	go connMitmer(inC, conf)

	// Just wait forever here so other goroutines live on
	// TODO: Gracefully exit some day
	done := make(chan bool)
	<-done

}
