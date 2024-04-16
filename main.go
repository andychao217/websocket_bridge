package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Connection struct {
	ws   *websocket.Conn
	lock sync.Mutex
}

func (c *Connection) SendMessage(message []byte) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.ws.WriteMessage(websocket.TextMessage, message); err != nil {
		fmt.Println("Error sending message:", err)
	}
}

func (c *Connection) Close() {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.ws.Close()
}

func handleHTMLConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Error upgrading connection:", err)
		return
	}
	defer ws.Close()

	// 从HTML接收外部WebSocket服务器地址
	var externalServerAddr string
	for {
		mt, message, err := ws.ReadMessage()
		fmt.Printf("message:%v\n", string(message))
		if err != nil {
			break
		}
		if mt == websocket.TextMessage {
			externalServerAddr = string(message)
			break
		}
	}

	// 与外部WebSocket服务器建立连接
	externalConn, _, err := websocket.DefaultDialer.Dial(externalServerAddr, nil)
	if err != nil {
		log.Printf("Error connecting to external server at %s: %v", externalServerAddr, err)
		return
	}
	defer externalConn.Close()

	// 消息转发循环
	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		for {
			mt, message, err := ws.ReadMessage()
			fmt.Printf("message:%v\n", string(message))
			if err != nil {
				return
			}
			externalConn.WriteMessage(mt, message)
		}
	}()

	go func() {
		defer wg.Done()
		for {
			mt, message, err := externalConn.ReadMessage()
			if err != nil {
				return
			}
			ws.WriteMessage(mt, message)
		}
	}()

	wg.Wait()
}

func main() {
	// http.HandleFunc("/websocket", handleHTMLConnections)
	// log.Fatal(http.ListenAndServe(":63000", nil))
	addr := ":63000" // 监听地址和端口
	// 使用net.Listen创建TCP监听器
	listener, err := net.Listen("tcp", addr)

	if err != nil {
		log.Fatal("Listen error:", err)
	}
	defer listener.Close()
	// 将监听器转换为*net.TCPListener
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		log.Fatal("Failed to type assert listener to *net.TCPListener")
	}

	// 获取监听的地址
	address := tcpListener.Addr()
	fmt.Printf("WebSocket server listening on %s\n", address.String())

	// 使用得到的监听器来创建一个HTTP服务器
	server := &http.Server{Addr: address.String()}
	http.HandleFunc("/websocket", handleHTMLConnections)

	// 启动HTTP服务器，这里使用Server的Serve方法而不是http.ListenAndServe
	if err := server.Serve(tcpListener); err != nil && err != http.ErrServerClosed {
		log.Fatal("Serve error:", err)
	}
}
