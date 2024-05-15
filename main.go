package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	clientConn *websocket.Conn
	urlsMutex  sync.Mutex
	msg        Message
)

type Message struct {
	Msg         string `json:"message"`
	Topics      string `json:"topics"`
	Host        string `json:"host"`
	ThingSecret string `json:"thingSecret"`
}

func main() {
	port := ":63000" // 监听地址和端口
	// 使用net.Listen创建TCP监听器
	listener, err := net.Listen("tcp", port)

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
	http.HandleFunc("/websocket", websocketHandler)

	// 启动HTTP服务器，这里使用Server的Serve方法而不是http.ListenAndServe
	if err := server.Serve(tcpListener); err != nil && err != http.ErrServerClosed {
		log.Fatal("Serve error:", err)
	}

}

func websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Error upgrading to WebSocket:", err)
		return
	}
	clientConn = conn

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("Error reading message from WebSocket:", err)
			return
		}
		// 解析 JSON 消息
		err = json.Unmarshal(message, &msg)
		if err != nil {
			fmt.Println("json unmarshal error:", err)
			continue
		}
		updateURLs(msg)
	}
}

func updateURLs(message Message) {
	// 发起 HTTP 请求获取 URL 列表的逻辑
	// 这里根据你的实际情况实现
	urlsMutex.Lock()
	defer urlsMutex.Unlock()

	msg := message.Msg
	host := message.Host
	thingSecret := message.ThingSecret
	topics := message.Topics
	topicList := strings.Split(topics, ";")
	if msg == "connect" {
		// 调用函数并处理响应
		connectWebSocket(topicList, host, thingSecret)
	}

}

func connectWebSocket(topics []string, hostName, thingKey string) {
	if len(topics) > 0 {
		for _, topic := range topics {
			url := "ws://" + hostName + ":8186" + topic + "?authorization=" + thingKey
			go func(url string) {
				for {
					wsConn, _, err := websocket.DefaultDialer.Dial(url, nil)
					if err != nil {
						fmt.Printf("Error connecting to WebSocket (%s): %v\n", url, err)
						time.Sleep(5 * time.Second) // 重连间隔
						continue
					}

					// 读取来自 WebSocket 连接的消息并推送到前端 JavaScript 的连接上
					go func(wsConn *websocket.Conn) {
						for {
							_, message, err := wsConn.ReadMessage()
							if err != nil {
								fmt.Printf("Error reading message from WebSocket (%s): %v\n", url, err)
								wsConn.Close()
								break
							}
							err = clientConn.WriteMessage(websocket.TextMessage, message)
							if err != nil {
								fmt.Printf("Error writing message to client WebSocket: %v\n", err)
								wsConn.Close()
								break
							}
						}
					}(wsConn)

					// 保持 WebSocket 连接开启
					// 如果连接断开，会在上面的循环中重连
				}
			}(url)
		}
	}

}
