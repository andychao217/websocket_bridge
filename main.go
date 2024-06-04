package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

// 全局变量来存储所有连接的 WebSocket 客户端
var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan []byte)
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// messageHistory 是一个包含 sync.RWMutex 和 map 的结构体，用于存储消息主题和内容组合的键以及对应的时间戳。
var messageHistory = struct {
	sync.RWMutex
	m map[string]time.Time
}{m: make(map[string]time.Time)}

// MQTT 消息处理器
// messagePubHandler 函数检查消息是否已经在过去2秒内收到过。如果是，则不广播该消息；如果不是，则更新消息历史记录并广播消息。
// 使用读写锁 sync.RWMutex 来确保在并发环境中安全地访问和修改 messageHistory。
var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	messageHistory.RLock()
	timestamp, exists := messageHistory.m[msg.Topic()+string(msg.Payload())]
	messageHistory.RUnlock()

	if exists && time.Since(timestamp) < 2*time.Second {
		fmt.Printf("Duplicate message received on topic %s: %s\n", msg.Topic(), msg.Payload())
	} else {
		fmt.Printf("Received message on topic %s: %s\n", msg.Topic(), msg.Payload())
		messageHistory.Lock()
		messageHistory.m[msg.Topic()+string(msg.Payload())] = time.Now()
		messageHistory.Unlock()
		broadcast <- msg.Payload()
	}
}

// WebSocket 消息结构
type WebSocketMessage struct {
	Topics      string `json:"topics"`
	Host        string `json:"host"`
	ThingSecret string `json:"thingSecret"`
	Message     string `json:"message"`
}

// WebSocket 处理器
func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	clients[ws] = true

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			log.Printf("error: %v", err)
			delete(clients, ws)
			break
		}
		handleMessage(ws, message)
	}
}

// 处理 WebSocket 消息
func handleMessage(ws *websocket.Conn, message []byte) {
	var msg WebSocketMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		return
	}

	if msg.Message == "connect" {
		topics := strings.Split(msg.Topics, ";")
		for _, topic := range topics {
			subscribeToMQTTTopic(msg.Host, ws.RemoteAddr().String(), msg.ThingSecret, topic+"/#")
		}
	}
}

// 广播消息给所有 WebSocket 客户端
func handleMessages() {
	for payload := range broadcast {
		fmt.Printf("Received payload: %s\n", string(payload))
		for client := range clients {
			err := client.WriteMessage(websocket.TextMessage, payload)
			if err != nil {
				log.Printf("error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

// 封装的 MQTT 连接和订阅函数
func subscribeToMQTTTopic(broker, clientID, thingSecret, topic string) {
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:1883", broker)).
		SetClientID(fmt.Sprintf("%s_%s", clientID, topic)).
		SetUsername(thingSecret).
		SetPassword(thingSecret).
		SetAutoReconnect(true).
		SetDefaultPublishHandler(messagePubHandler)

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		fmt.Printf("Failed to connect to MQTT broker: %v\n", token.Error())
		return
	}

	if token := client.Subscribe(topic, 0, nil); token.Wait() && token.Error() != nil {
		fmt.Printf("Failed to subscribe to MQTT topic: %v\n", token.Error())
		return
	} else {
		fmt.Printf("Successfully subscribed to MQTT topic: %v\n", topic)
	}
}

func main() {
	// 设置 WebSocket 处理器
	http.HandleFunc("/websocket", handleConnections)

	// 启动一个 goroutine 来处理消息广播
	go handleMessages()

	port := os.Getenv("MG_WEBSOCKET_BRIDGE_PORT")
	if port == "" {
		port = "63000" // 默认端口
	}
	// 启动 HTTP 服务器，监听端口 63000
	log.Println("HTTP server started on :" + port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
