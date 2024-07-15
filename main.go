package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

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
var minioClient *minio.Client // MinIO 客户端
var bucketName string         // 存储桶名称

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

// clearPbMsgs 清空 pbMsgs
func clearPbMsgs() {
	mutex.Lock()
	defer mutex.Unlock()
	pbMsgs = nil
}

// startUDPListener 启动一个 UDP 组播侦听器-扫描设备
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

	// 启动定时器，每隔15秒清空一次 pbMsgs
	go func() {
		for {
			time.Sleep(15 * time.Second)
			clearPbMsgs()
		}
	}()

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

	if msgIdName == "DEVICE_ADVERTISE" {
		var unmarshaledData interface{} // 使用空接口来存储不同类型的数据
		var temp proto.DeviceAdvertiseData
		err = gProto.Unmarshal(data, &temp)
		if err != nil {
			fmt.Println("解析错误:", err)
			return
		}
		deviceName := temp.DeviceName
		unmarshaledData = &temp

		jsonBytes, err := json.Marshal(unmarshaledData)
		if err != nil {
			fmt.Println("转换为 JSON 时发生错误:", err)
			return
		}

		// 将字节切片转换为字符串
		jsonString := string(jsonBytes)
		fmt.Println("JSON 字符串:", jsonString)

		// 检查 pbMsgs 中是否已经存在相同的 jsonString或者是否有相同的deviceName，如果有就用新的替换旧的
		exists := false
		for i, msg := range pbMsgs {
			if msg.Message == jsonString || strings.Contains(msg.Message, deviceName) {
				// 更新找到的 PbMsg
				pbMsgs[i] = PbMsg{
					Timestamp:   time.Now(),
					Message:     jsonString,
					MessageType: msgIdName,
				}
				exists = true
				break
			}
		}

		// 如果不存在相同的 jsonString，则添加新的 PbMsg
		if !exists {
			pbMsg := PbMsg{
				Timestamp:   time.Now(),
				Message:     jsonString,
				MessageType: msgIdName,
			}
			pbMsgs = append(pbMsgs, pbMsg)
		}
	}
}

// 返回UDP扫描到的设备信息
func getDevicesHandler(w http.ResponseWriter, r *http.Request) {
	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

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

// 平台系统添加设备成功后，给设备回一个确认消息
func addDeviceReplyHandler(w http.ResponseWriter, r *http.Request) {
	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var reqData []*proto.DeviceAdvertiseData
	err := json.NewDecoder(r.Body).Decode(&reqData)
	if err != nil {
		http.Error(w, "Failed to decode JSON", http.StatusBadRequest)
		return
	}

	for _, item := range reqData {
		// 序列化protobuf消息
		dataBuf, err := gProto.Marshal(item)
		if err != nil {
			http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
			return
		}

		fmt.Println("dataBuf: ", item.DeviceName)
		fmt.Println("TenantId: ", item.TenantId)

		// 创建protobuf消息
		pbMsg := &proto.PbMsg{
			Id:   380,
			Data: dataBuf,
		}

		fmt.Println("pbMsg: ", pbMsg.Id)

		// 序列化protobuf消息
		buf, err := gProto.Marshal(pbMsg)
		if err != nil {
			http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
			return
		}

		// 发送UDP数据
		sendUDP(buf)
	}

	fmt.Fprintf(w, "Data sent via UDP")
}

// 发送UDP信息
func sendUDP(data []byte) {
	port := os.Getenv("MG_SOCKET_BRIDGE_UDP_PORT")
	if port == "" {
		port = "60000" // 默认端口
	}
	ip := os.Getenv("MG_SOCKET_BRIDGE_UDP_IP")
	if ip == "" {
		ip = "232.0.0.254" // 默认ip地址
	}
	serverAddr, err := net.ResolveUDPAddr("udp", ip+":"+port)
	if err != nil {
		fmt.Println("ResolveUDPAddr failed:", err)
		return
	}

	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		fmt.Println("DialUDP failed:", err)
		return
	}
	defer conn.Close()

	_, err = conn.Write(data)
	if err != nil {
		fmt.Println("Write data failed:", err)
		return
	}

	fmt.Println("UDP message sent")
}

// 生成mqtt clientID字符串
func generateClientID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	clientID := make([]byte, 20)
	for i := range clientID {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		clientID[i] = charset[num.Int64()]
	}
	return string(clientID)
}

// 给设备发送重启指令
func rebootDeviceHandler(w http.ResponseWriter, r *http.Request) {
	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	// 处理 OPTIONS 请求
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 处理 POST 请求
	if r.Method == http.MethodPost {
		// 这里可以添加处理 POST 请求的逻辑
		var params struct {
			ChannelID     string `json:"channelID"`
			ThingIdentity string `json:"thingIdentity"`
			Host          string `json:"host"`
			ComID         string `json:"comID"`
		}

		err := json.NewDecoder(r.Body).Decode(&params)
		if err != nil {
			http.Error(w, "Error decoding request body", http.StatusBadRequest)
			return
		}

		fmt.Printf("ChannelID: %v\n", params.ChannelID)
		fmt.Printf("ThingIdentity: %v\n", params.ThingIdentity)
		fmt.Printf("Host: %v\n", params.Host)
		fmt.Printf("ComID: %v\n", params.ComID)

		// 构建 MQTT 主题
		topic := fmt.Sprintf("channels/%s/messages/%s", params.ChannelID, params.ThingIdentity)

		// 生成随机字符串作为 MQTT 客户端 ID
		clientID := generateClientID()

		fmt.Println("clientID: ", clientID)

		// MQTT 配置
		opts := mqtt.NewClientOptions().
			AddBroker(fmt.Sprintf("tcp://%s:1883", params.Host)).
			SetClientID(clientID).
			SetUsername("platform" + params.ComID).
			SetPassword("platform" + params.ComID).
			SetAutoReconnect(true)

		// 创建 MQTT 客户端
		client := mqtt.NewClient(opts)
		if token := client.Connect(); token.Wait() && token.Error() != nil {
			fmt.Printf("Failed to connect to MQTT broker: %v\n", token.Error())
			return
		}

		defer client.Disconnect(200)

		var reqData = proto.DeviceReboot{
			Username: "platform" + params.ComID,
		}
		// 序列化protobuf消息
		dataBuf, err := gProto.Marshal(&reqData)
		if err != nil {
			http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
			return
		}
		// 创建protobuf消息
		pbMsg := &proto.PbMsg{
			Id:   359,
			Data: dataBuf,
		}
		// 序列化protobuf消息
		buf, err := gProto.Marshal(pbMsg)
		if err != nil {
			http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
			return
		}

		fmt.Println("DeviceReboot: ", buf)

		// 发送 MQTT 消息
		token := client.Publish(strings.Replace(topic, " ", "", -1), 0, false, buf)
		token.Wait()
		if token.Error() != nil {
			http.Error(w, "Error sending MQTT message", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Message sent to topic: %s\n", topic)

		jsonBytes, err := json.Marshal(&reqData)
		if err != nil {
			fmt.Println("转换为 JSON 时发生错误:", err)
			return
		}
		// 将字节切片转换为字符串
		jsonString := string(jsonBytes)
		fmt.Println("DeviceReboot: ", jsonString)

		jsonBytes, err = json.Marshal(pbMsg)
		if err != nil {
			fmt.Println("转换为 JSON 时发生错误:", err)
			return
		}
		// 将字节切片转换为字符串
		jsonString = string(jsonBytes)
		fmt.Println("PbMsg: ", jsonString)

		fmt.Println("Message sent to topic: ", topic)
		return

	}

	// 如果是其他方法，则返回方法不允许的错误
	http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
}

// 获取本机 IP 地址
func getLocalIPAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, inter := range interfaces {
		if inter.Flags&net.FlagUp == 0 {
			continue
		}
		if inter.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := inter.Addrs()
		if err != nil {
			return "", err
		}

		for _, addr := range addrs {
			ip, ok := addr.(*net.IPNet)
			if !ok || ip.IP.IsLoopback() {
				continue
			}
			if ip.IP.To4() != nil {
				return ip.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no active network interfaces found")
}

// 初始化 MinIO 客户端
func initMinio() {
	// 初始化 MinIO 客户端
	var err error
	localIP, err := getLocalIPAddress()
	if err != nil {
		log.Fatalf("Failed to get local IP address: %v", err)
	}
	endpoint := localIP + ":9100"

	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "admin"
	}
	secretKey := os.Getenv("MINIO_SECRET_KEY")
	if secretKey == "" {
		secretKey = "12345678"
	}
	bucketName = os.Getenv("MINIO_BUCKET_NAME")
	if bucketName == "" {
		bucketName = "nxt-tenant"
	}

	minioClient, err = minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})

	if err != nil {
		log.Fatalln(err)
	}

	// 确保 bucket 存在
	err = minioClient.MakeBucket(context.Background(), bucketName, minio.MakeBucketOptions{})
	if err != nil {
		exists, errBucketExists := minioClient.BucketExists(context.Background(), bucketName)
		if errBucketExists == nil && exists {
			fmt.Printf("We already own %s\n", bucketName)
		} else {
			log.Fatalln(err)
		}
	}
}

// 生成日程列表
func buildTaskList(client *minio.Client, bucket, prefix string) []*proto.Task {
	opts := minio.ListObjectsOptions{
		Recursive: false,
		Prefix:    prefix,
	}
	ctx := context.Background()

	objectCh := client.ListObjects(ctx, bucket, opts)

	var tasks []*proto.Task
	for object := range objectCh {
		if object.Err != nil {
			log.Println(object.Err)
			continue
		}

		objectName := object.Key
		obj, err := minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
		if err != nil {
			log.Println(err)
			continue
		}

		data, err := io.ReadAll(obj)
		obj.Close() // 确保在读取完数据后立即关闭对象
		if err != nil {
			log.Println(err)
			continue
		}

		var task proto.Task
		if err := gProto.Unmarshal(data, &task); err != nil {
			log.Println(err)
			continue
		}

		tasks = append(tasks, &task)
	}
	return tasks
}

// 获取日程列表
func getTaskListHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	type GetResourceListRequest struct {
		Path  string `json:"path"`
		ComID string `json:"comID"`
	}

	var request GetResourceListRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}

	// 构建日程列表
	taskList := buildTaskList(minioClient, bucketName, prefix)

	jsonData, err := json.MarshalIndent(taskList, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(jsonData)
}

func main() {
	// 设置 WebSocket 处理器
	http.HandleFunc("/websocket", handleConnections)
	// 启动一个 goroutine 来处理消息广播
	go handleMessages()

	// 启动 UDP 组播侦听器
	go startUDPListener()

	http.HandleFunc("/devices", getDevicesHandler)            // 获取设备列表
	http.HandleFunc("/rebootDevice", rebootDeviceHandler)     // 重启设备
	http.HandleFunc("/addDeviceReply", addDeviceReplyHandler) // 添加设备回复

	initMinio() // 初始化MinIO
	http.HandleFunc("/taskList", getTaskListHandler)

	port := os.Getenv("MG_SOCKET_BRIDGE_PORT")
	if port == "" {
		port = "63001" // 默认端口
	}

	// 启动 HTTP 服务器，监听端口 63001`
	log.Println("HTTP server started on :" + port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
