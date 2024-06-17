package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"

	proto "github.com/andychao217/magistrala-websocket_bridge/proto"
	gProto "google.golang.org/protobuf/proto"
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

// PbMsg 代表设备信息的结构体
type PbMsg struct {
	Timestamp   time.Time `json:"timestamp"`
	Message     string    `json:"message"`
	MessageType string    `json:"message_type"`
}

// 全局变量，用于存储接收到的设备信息
var (
	pbMsgs []PbMsg
	mutex  sync.Mutex
)

// startUDPListener 启动一个 UDP 组播侦听器
func startUDPListener() {
	port := os.Getenv("MG_SOCKET_BRIDGE_UDP_PORT")
	if port == "" {
		port = "60000" // 默认端口
	}
	ip := os.Getenv("MG_SOCKET_BRIDGE_UDP_IP")
	if ip == "" {
		ip = "232.0.0.254" // 默认ip地址
	}
	addr, err := net.ResolveUDPAddr("udp", ip+":"+port) // 替换为你的组播地址和端口
	if err != nil {
		panic(err)
	}

	conn, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	buf := make([]byte, 1024)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("Error reading from UDP:", err)
			continue
		}
		savePbMsg(buf[:n])
	}
}

// savePbMsg 保存设备信息到全局变量
func savePbMsg(message []byte) {
	mutex.Lock()
	defer mutex.Unlock()

	var receivedMsg proto.PbMsg

	// 使用 gProto.Unmarshal 解析数据到消息实例
	err := gProto.Unmarshal(message, &receivedMsg)
	if err != nil {
		fmt.Println("解析错误:", err)
		return
	}

	var msgIdName string = proto.MsgId_name[int32(receivedMsg.Id)]
	var data []byte = receivedMsg.Data

	var unmarshaledData interface{} // 使用空接口来存储不同类型的数据

	fmt.Println("msgIdName: ", msgIdName)

	if msgIdName == "DEVICE_ADVERTISE" {
		var temp proto.DeviceAdvertiseData
		err = gProto.Unmarshal(data, &temp)
		if err != nil {
			fmt.Println("解析错误:", err)
			return
		}
		unmarshaledData = &temp
	}
	// else if msgIdName == "RADIO_FREQ_GET" {
	// 	var temp proto.RadioFreqPack
	// 	err = gProto.Unmarshal(data, &temp)
	// 	if err != nil {
	// 		fmt.Println("解析错误:", err)
	// 		return
	// 	}
	// 	unmarshaledData = &temp
	// }

	// 检查 unmarshaledData 是否已经被赋值
	if unmarshaledData == nil {
		fmt.Println("未找到匹配的消息类型")
		return
	}

	jsonBytes, err := json.Marshal(unmarshaledData)
	if err != nil {
		fmt.Println("转换为 JSON 时发生错误:", err)
		return
	}

	// 将字节切片转换为字符串
	jsonString := string(jsonBytes)
	fmt.Println("JSON 字符串:", jsonString)

	pbMsg := PbMsg{
		Timestamp:   time.Now(),
		Message:     jsonString,
		MessageType: msgIdName,
	}
	pbMsgs = append(pbMsgs, pbMsg)
}

// getDevicesHandler 处理 HTTP 请求，返回接收到的设备信息
func getDevicesHandler(w http.ResponseWriter, r *http.Request) {
	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if r.Method == http.MethodOptions {
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	// 将 pbMsgs 转换为 JSON
	jsonData, err := json.Marshal(pbMsgs)
	if err != nil {
		http.Error(w, "Error marshalling to JSON", http.StatusInternalServerError)
		return
	}

	if len(pbMsgs) > 0 {
		// 将 JSON 数据写入响应
		w.Write(jsonData)
	} else {
		w.Write([]byte(""))
	}
}

func main() {
	// 设置 WebSocket 处理器
	http.HandleFunc("/websocket", handleConnections)

	// 启动一个 goroutine 来处理消息广播
	go handleMessages()

	// 启动 UDP 组播侦听器
	go startUDPListener()
	http.HandleFunc("/devices", getDevicesHandler)

	port := os.Getenv("MG_SOCKET_BRIDGE_PORT")
	if port == "" {
		port = "6300`" // 默认端口
	}

	// 启动 HTTP 服务器，监听端口 63001`
	log.Println("HTTP server started on :" + port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
