package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
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
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
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

// 生成mqtt clientID字符串
func generateClientID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	clientID := make([]byte, 10)
	for i := range clientID {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		clientID[i] = charset[num.Int64()]
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10) // 将时间戳转换为字符串
	return string(clientID) + timestamp
}

// MQTT 消息处理器
// messagePubHandler 函数检查消息是否已经在过去2秒内收到过。如果是，则不广播该消息；如果不是，则更新消息历史记录并广播消息。
// 使用读写锁 sync.RWMutex 来确保在并发环境中安全地访问和修改 messageHistory。
var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	messageHistory.RLock()
	timestamp, exists := messageHistory.m[msg.Topic()+string(msg.Payload())]
	messageHistory.RUnlock()

	if exists && time.Since(timestamp) < 2*time.Second {
		// fmt.Printf("Duplicate message received on topic %s: %s\n", msg.Topic(), msg.Payload())
	} else {
		// fmt.Printf("Received message on topic %s: %s\n", msg.Topic(), msg.Payload())
		messageHistory.Lock()
		messageHistory.m[msg.Topic()+string(msg.Payload())] = time.Now()
		messageHistory.Unlock()
		broadcast <- msg.Payload()
	}
}

// WebSocket 消息结构
type WebSocketMessage struct {
	Topics      []string `json:"topics"`
	Host        string   `json:"host"`
	ThingSecret string   `json:"thingSecret"`
	Message     string   `json:"message"`
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
		handleWebsocketMessage(message)
	}
}

// 处理 WebSocket 消息
func handleWebsocketMessage(message []byte) {
	var msg WebSocketMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Error parsing JSON: %v", err)
		return
	}

	if msg.Message == "connect" {
		for _, topic := range msg.Topics {
			subscribeToMQTTTopic(msg.Host, msg.ThingSecret, topic+"/#")
		}
	}
}

// 广播消息给所有 WebSocket 客户端
func handleMessages() {
	type PayloadData struct {
		Data    interface{} `json:"data"`
		Source  string      `json:"source"`
		MsgName string      `json:"msgName"`
	}

	// 定义一个解析函数类型
	type msgParser func([]byte, interface{}) error

	// 映射消息ID到解析函数
	msgParsers := map[string]msgParser{
		"TASK_START":            func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStart)) },
		"TASK_STOP":             func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStop)) },
		"TASK_STATUS_GET":       func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStatusGet)) },
		"TASK_STATUS_GET_REPLY": func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStatusGetReply)) },
		"TASK_SYNC_STATUS_GET":  func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskSyncStatusGet)) },
		"TASK_SYNC_STATUS_GET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.TaskSyncStatusGetReply))
		},
		"GET_LOG_REPLY":           func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.GetLogReply)) },
		"DEVICE_LOGIN":            func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceLogin)) },
		"DEVICE_INFO_GET_REPLY":   func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceInfoGetReply)) },
		"DEVICE_INFO_UPDATE":      func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceInfoUpdate)) },
		"DEVICE_RESTORE_REPLY":    func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceRestoreReply)) },
		"DEVICE_ALIASE_SET_REPLY": func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceAliaseSetReply)) },
		"LED_CFG_SET_REPLY":       func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.LedCfgSetReply)) },
		"STEREO_CFG_SET_REPLY":    func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.StereoCfgSetReply)) },
		"OUT_CHANNEL_EDIT_REPLY":  func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.OutChannelEditReply)) },
		"IN_CHANNEL_EDIT_REPLY":   func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.InChannelEditReply)) },
		"BLUETOOTH_CFG_SET_REPLY": func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.BluetoothCfgSetReply)) },
		"SPEECH_CFG_SET_REPLY":    func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.SpeechCfgSetReply)) },
		"BLUETOOTH_WHITELIST_ADD_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.BluetoothWhitelistAddReply))
		},
		"BLUETOOTH_WHITELIST_DELETE_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.BluetoothWhitelistDeleteReply))
		},
		"AMP_CHECK_CFG_SET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.AmpCheckCfgSetReply))
		},
		"AUDIO_MATRIX_CFG_SET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.AudioMatrixCfgSetReply))
		},
		"RADIO_FREQ_GET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.RadioFreqGetReply))
		},
		"RADIO_FREQ_ADD_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.RadioFreqAddReply))
		},
		"RADIO_FREQ_SET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.RadioFreqSetReply))
		},
		"RADIO_FREQ_DELETE_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.RadioFreqDeleteReply))
		},
		"U_CHANNEL_SET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.UChannelSetReply))
		},
		"EQ_CFG_SET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.EqCfgSetReply))
		},
		"SPEAKER_VOLUME_SET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.SpeakerVolumeSetReply))
		},
	}

	for payload := range broadcast {
		var receivedMsg proto.PbMsg
		err := gProto.Unmarshal(payload, &receivedMsg)
		if err != nil {
			fmt.Println("解析错误:", err)
			continue
		}

		msgIdName := proto.MsgId_name[int32(receivedMsg.Id)]
		data := receivedMsg.Data

		unmarshaledData := PayloadData{MsgName: msgIdName, Source: receivedMsg.Source}

		// 使用映射解析消息
		if parser, exists := msgParsers[msgIdName]; exists {
			// 为每种消息类型创建具体的变量
			var msgData interface{}
			switch msgIdName {
			case "TASK_START":
				msgData = &proto.TaskStart{}
			case "TASK_STOP":
				msgData = &proto.TaskStop{}
			case "TASK_STATUS_GET":
				msgData = &proto.TaskStatusGet{}
			case "TASK_STATUS_GET_REPLY":
				msgData = &proto.TaskStatusGetReply{}
			case "TASK_SYNC_STATUS_GET":
				msgData = &proto.TaskSyncStatusGet{}
			case "TASK_SYNC_STATUS_GET_REPLY":
				msgData = &proto.TaskSyncStatusGetReply{}
			case "GET_LOG_REPLY":
				msgData = &proto.GetLogReply{}
			case "DEVICE_LOGIN":
				msgData = &proto.DeviceLogin{}
			case "DEVICE_INFO_GET_REPLY":
				msgData = &proto.DeviceInfoGetReply{}
			case "DEVICE_INFO_UPDATE":
				msgData = &proto.DeviceInfoUpdate{}
			case "DEVICE_RESTORE_REPLY":
				msgData = &proto.DeviceRestoreReply{}
			case "DEVICE_ALIASE_SET_REPLY":
				msgData = &proto.DeviceAliaseSetReply{}
			case "OUT_CHANNEL_EDIT_REPLY":
				msgData = &proto.OutChannelEditReply{}
			case "IN_CHANNEL_EDIT_REPLY":
				msgData = &proto.InChannelEditReply{}
			case "LED_CFG_SET_REPLY":
				msgData = &proto.LedCfgSetReply{}
			case "AMP_CHECK_CFG_SET_REPLY":
				msgData = &proto.AmpCheckCfgSetReply{}
			case "AUDIO_MATRIX_CFG_SET_REPLY":
				msgData = &proto.AudioMatrixCfgSetReply{}
			case "STEREO_CFG_SET_REPLY":
				msgData = &proto.StereoCfgSetReply{}
			case "BLUETOOTH_CFG_SET_REPLY":
				msgData = &proto.BluetoothCfgSetReply{}
			case "SPEECH_CFG_SET_REPLY":
				msgData = &proto.SpeechCfgSetReply{}
			case "BLUETOOTH_WHITELIST_ADD_REPLY":
				msgData = &proto.BluetoothWhitelistAddReply{}
			case "BLUETOOTH_WHITELIST_DELETE_REPLY":
				msgData = &proto.BluetoothWhitelistDeleteReply{}
			case "RADIO_FREQ_GET_REPLY":
				msgData = &proto.RadioFreqGetReply{}
			case "RADIO_FREQ_ADD_REPLY":
				msgData = &proto.RadioFreqAddReply{}
			case "RADIO_FREQ_SET_REPLY":
				msgData = &proto.RadioFreqSetReply{}
			case "RADIO_FREQ_DELETE_REPLY":
				msgData = &proto.RadioFreqDeleteReply{}
			case "U_CHANNEL_SET_REPLY":
				msgData = &proto.UChannelSetReply{}
			case "EQ_CFG_SET_REPLY":
				msgData = &proto.EqCfgSetReply{}
			case "SPEAKER_VOLUME_SET_REPLY":
				msgData = &proto.SpeakerVolumeSetReply{}
			default:
				fmt.Println("未知的消息类型:", msgIdName)
				continue
			}

			err := parser(data, msgData)
			if err != nil {
				fmt.Println("解析错误:", err)
				continue
			}
			unmarshaledData.Data = msgData
		} else {
			fmt.Println("未知的消息类型:", msgIdName)
			continue
		}

		jsonBytes, err := json.Marshal(unmarshaledData)
		if err != nil {
			fmt.Println("转换为 JSON 时发生错误:", err)
			continue
		}

		jsonString := string(jsonBytes)

		for client := range clients {
			// fmt.Println("websocket.TextMessage: ", jsonString)
			err := client.WriteMessage(websocket.TextMessage, []byte(jsonString))
			if err != nil {
				log.Printf("error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

// 封装的 MQTT 连接和订阅函数
func subscribeToMQTTTopic(broker, thingSecret, topic string) {
	clientID := thingSecret + "_" + generateClientID()
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:1883", broker)).
		SetClientID(clientID).
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

		// 创建protobuf消息
		pbMsg := &proto.PbMsg{
			Id:   380,
			Data: dataBuf,
		}

		// 序列化protobuf消息
		buf, err := gProto.Marshal(pbMsg)
		if err != nil {
			http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
			return
		}

		// 发送UDP数据
		sendUDP(buf)
	}
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
}

// 创建请求数据
func createRequestData(params *struct {
	ControlType        string
	Username           string
	Task               *proto.Task // 修改为指针类型
	Uuid               string
	ComID              string
	DeviceInfo         *proto.DeviceInfo
	ChannelAttr        *proto.ChannelAttr
	SpeechCfg          *proto.SpeechCfg
	DevicePowerPack    *proto.DevicePowerPack
	UChannel           int32
	EqCfg              *proto.EqCfg
	SpeakerVolume      *proto.SpeakerVolume
	AudioMatrix        *proto.AudioMatrix
	RadioFreq          *proto.RadioFreq
	BluetoothCfg       *proto.BluetoothCfg
	BluetoothWhitelist *proto.BluetoothWhitelist
	Enabled            bool
}) (gProto.Message, proto.MsgId, error) {
	var reqData gProto.Message
	var pbMsgId proto.MsgId

	switch params.ControlType {
	case "taskAdd", "taskEdit", "taskDelete":
		var taskSyncType proto.TaskSyncType
		if params.ControlType == "taskAdd" {
			taskSyncType = 0
		} else if params.ControlType == "taskEdit" {
			taskSyncType = 2
		} else if params.ControlType == "taskDelete" {
			taskSyncType = 1
		}
		reqData = &proto.TaskSync{Username: params.Username, Type: taskSyncType, TaskUuid: params.Uuid} // 直接使用指针
		pbMsgId = 312
	case "taskStart":
		reqData = &proto.TaskStart{Username: params.Username, Task: params.Task} // 直接使用指针
		pbMsgId = 231
	case "taskStop":
		reqData = &proto.TaskStop{Username: params.Username, Uuid: params.Uuid}
		pbMsgId = 233
	case "taskStatGet":
		reqData = &proto.TaskStatusGet{Username: params.Username}
		pbMsgId = 314
	case "taskSyncStatusGet":
		reqData = &proto.TaskSyncStatusGet{Username: params.Username, TaskUuid: params.Uuid}
		pbMsgId = 333
	case "deviceReboot":
		reqData = &proto.DeviceReboot{Username: "platform" + params.ComID}
		pbMsgId = 359
	case "deviceInfoGet":
		reqData = &proto.DeviceInfoGet{Username: params.Username}
		pbMsgId = 229
	case "deviceInfoSet":
		reqData = &proto.DeviceInfoSet{Username: params.Username, Info: params.DeviceInfo}
		pbMsgId = 302
	case "deviceAliaseSet":
		reqData = &proto.DeviceAliaseSet{Username: params.Username, DeviceAliase: params.DeviceInfo.DeviceAliase}
		pbMsgId = 331
	case "deviceOutChannelEdit":
		reqData = &proto.OutChannelEdit{Username: params.Username, Attr: params.ChannelAttr}
		pbMsgId = 265
	case "deviceInChannelEdit":
		reqData = &proto.InChannelEdit{Username: params.Username, Attr: params.ChannelAttr}
		pbMsgId = 269
	case "deviceRestore":
		reqData = &proto.DeviceRestore{Username: params.Username}
		pbMsgId = 304
	case "deviceLEDSet":
		reqData = &proto.LedCfgSet{Username: params.Username, LedEnable: params.Enabled}
		pbMsgId = 336
	case "deviceAmpCheckSet":
		reqData = &proto.AmpCheckCfgSet{Username: params.Username, AmpCheckEnable: params.Enabled}
		pbMsgId = 344
	case "deviceStereoSet":
		reqData = &proto.StereoCfgSet{Username: params.Username, StereoEnable: params.Enabled}
		pbMsgId = 329
	case "devicePowerCfgSet":
		reqData = &proto.DevicePowerSet{Username: params.Username, Out_1Power: params.DevicePowerPack.Out_1Power, Out_2Power: params.DevicePowerPack.Out_2Power, Out_3Power: params.DevicePowerPack.Out_3Power, Out_4Power: params.DevicePowerPack.Out_4Power}
		pbMsgId = 310
	case "deviceAudioMatrixCfgSet":
		reqData = &proto.AudioMatrixCfgSet{Username: params.Username, AudioMatrix: params.AudioMatrix}
		pbMsgId = 373
	case "deviceRadioFreqGet":
		reqData = &proto.RadioFreqGet{Username: params.Username}
		pbMsgId = 294
	case "deviceRadioFreqAdd":
		reqData = &proto.RadioFreqAdd{Username: params.Username, Rf: params.RadioFreq}
		pbMsgId = 296
	case "deviceRadioFreqSet":
		reqData = &proto.RadioFreqSet{Username: params.Username, Rf: params.RadioFreq}
		pbMsgId = 298
	case "deviceRadioFreqDelete":
		reqData = &proto.RadioFreqDelete{Username: params.Username, Rf: params.RadioFreq}
		pbMsgId = 300
	case "deviceUChannelSet":
		reqData = &proto.UChannelSet{UChannel: params.UChannel}
		pbMsgId = 397
	case "deviceEqCfgSet":
		reqData = &proto.EqCfgSet{EqCfg: params.EqCfg}
		pbMsgId = 395
	case "deviceSpeakerVolumeSet":
		reqData = &proto.SpeakerVolumeSet{Volume: params.SpeakerVolume}
		pbMsgId = 391
	case "deviceSpeechCfgSet":
		reqData = &proto.SpeechCfgSet{Username: params.Username, SpeechCfg: params.SpeechCfg}
		pbMsgId = 340
	case "deviceBluetoothCfgSet":
		reqData = &proto.BluetoothCfgSet{Username: params.Username, BluetoothCfg: params.BluetoothCfg}
		pbMsgId = 348
	case "deviceBluetoothWhitelistAdd":
		reqData = &proto.BluetoothWhitelistAdd{Username: params.Username, Whitelist: params.BluetoothWhitelist}
		pbMsgId = 352
	case "deviceBluetoothWhitelistDelete":
		reqData = &proto.BluetoothWhitelistDelete{Username: params.Username, Whitelist: params.BluetoothWhitelist}
		pbMsgId = 354
	case "deviceLogGet":
		reqData = &proto.GetLog{Username: params.Username, Type: 0}
		pbMsgId = 247
	default:
		return nil, 0, fmt.Errorf("invalid control type: %s", params.ControlType)
	}

	return reqData, pbMsgId, nil
}

// 给设备发送指令
func controlDeviceHandler(w http.ResponseWriter, r *http.Request) {
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

	// 这里可以添加处理 POST 请求的逻辑
	var params struct {
		ChannelID          string                    `json:"channelID"`
		ThingIdentity      string                    `json:"thingIdentity"`
		Host               string                    `json:"host"`
		ComID              string                    `json:"comID"`
		ControlType        string                    `json:"controlType"`
		Uuid               string                    `json:"uuid"`
		Task               *proto.Task               `json:"task"`
		Username           string                    `json:"username"`
		DeviceInfo         *proto.DeviceInfo         `json:"deviceInfo"`
		ChannelAttr        *proto.ChannelAttr        `json:"channelAttr"`
		SpeechCfg          *proto.SpeechCfg          `json:"speechCfg"`
		DevicePowerPack    *proto.DevicePowerPack    `json:"devicePowerPack"`
		UChannel           int32                     `json:"uChannel"`
		EqCfg              *proto.EqCfg              `json:"eqCfg"`
		SpeakerVolume      *proto.SpeakerVolume      `json:"speakerVolume"`
		AudioMatrix        *proto.AudioMatrix        `json:"audioMatrix"`
		RadioFreq          *proto.RadioFreq          `json:"radioFreq"`
		BluetoothCfg       *proto.BluetoothCfg       `json:"bluetoothCfg"`
		BluetoothWhitelist *proto.BluetoothWhitelist `json:"bluetoothWhitelist"`
		Enabled            bool                      `json:"enabled"`
	}

	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		http.Error(w, "Error decoding request body", http.StatusBadRequest)
		return
	}

	// fmt.Printf("ChannelID: %v\n", params.ChannelID)
	// fmt.Printf("ThingIdentity: %v\n", params.ThingIdentity)
	// fmt.Printf("Host: %v\n", params.Host)
	// fmt.Printf("ComID: %v\n", params.ComID)

	// 构建 MQTT 主题
	topic := fmt.Sprintf("channels/%s/messages/%s", params.ChannelID, params.ThingIdentity)
	if params.ControlType == "taskStatGet" {
		// 获取任务状态
		topic = fmt.Sprintf("channels/%s/messages/common/device", params.ChannelID)
	}
	nameAndPass := "platform" + params.ComID
	// 生成随机字符串作为 MQTT 客户端 ID
	clientID := nameAndPass + "_" + generateClientID()

	// fmt.Println("clientID 123: ", clientID)

	// MQTT 配置
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:1883", params.Host)).
		SetClientID(clientID).
		SetUsername(nameAndPass).
		SetPassword(nameAndPass).
		SetAutoReconnect(true)

	// 创建 MQTT 客户端
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		fmt.Printf("Failed to connect to MQTT broker: %v\n", token.Error())
		return
	}

	defer client.Disconnect(200)

	// 创建一个新的结构体，传递所需字段
	reqParams := &struct {
		ControlType        string
		Username           string
		Task               *proto.Task // 修改为指针类型
		Uuid               string
		ComID              string
		DeviceInfo         *proto.DeviceInfo
		ChannelAttr        *proto.ChannelAttr
		SpeechCfg          *proto.SpeechCfg
		DevicePowerPack    *proto.DevicePowerPack
		UChannel           int32
		EqCfg              *proto.EqCfg
		SpeakerVolume      *proto.SpeakerVolume
		AudioMatrix        *proto.AudioMatrix
		RadioFreq          *proto.RadioFreq
		BluetoothCfg       *proto.BluetoothCfg
		BluetoothWhitelist *proto.BluetoothWhitelist
		Enabled            bool
	}{
		ControlType:        params.ControlType,
		Username:           params.Username,
		Task:               params.Task, // 直接使用指针
		Uuid:               params.Uuid,
		ComID:              params.ComID,
		DeviceInfo:         params.DeviceInfo,
		ChannelAttr:        params.ChannelAttr,
		SpeechCfg:          params.SpeechCfg,
		DevicePowerPack:    params.DevicePowerPack,
		UChannel:           params.UChannel,
		EqCfg:              params.EqCfg,
		SpeakerVolume:      params.SpeakerVolume,
		AudioMatrix:        params.AudioMatrix,
		RadioFreq:          params.RadioFreq,
		BluetoothCfg:       params.BluetoothCfg,
		BluetoothWhitelist: params.BluetoothWhitelist,
		Enabled:            params.Enabled,
	}
	// 创建请求数据
	reqData, pbMsgId, err := createRequestData(reqParams)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 序列化protobuf消息
	dataBuf, err := gProto.Marshal(reqData)
	if err != nil {
		http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
		return
	}

	// 创建protobuf消息
	pbMsg := &proto.PbMsg{
		Id:     pbMsgId,
		Data:   dataBuf,
		Source: "web_" + params.ComID,
	}
	// 序列化protobuf消息
	buf, err := gProto.Marshal(pbMsg)
	if err != nil {
		http.Error(w, "Failed to marshal protobuf", http.StatusInternalServerError)
		return
	}

	// 发送 MQTT 消息
	token := client.Publish(strings.Replace(topic, " ", "", -1), 0, false, buf)
	token.Wait()
	if token.Error() != nil {
		http.Error(w, "Error sending MQTT message", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)

	type ControlDevcieResponse struct {
		Message string `json:"message"`
	}
	// 创建响应数据
	response := ControlDevcieResponse{
		Message: "Message sent to topic: " + topic,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
			// fmt.Printf("We already own %s\n", bucketName)
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

// 日程请求结构体-添加、更新、复制
type TaskRequest struct {
	Path  string     `json:"path"`
	ComID string     `json:"comID"`
	Task  proto.Task `json:"task"`
}

// 日程响应结构体
type TaskResponse struct {
	Message string      `json:"message"`
	Task    *proto.Task `json:"task"`
}

// 检查日程文件是否已存在
func CheckFileExists(path, uuid string) error {
	// 构建文件路径
	filePath := path + uuid

	// 检查文件是否存在
	_, err := minioClient.StatObject(context.Background(), bucketName, filePath, minio.StatObjectOptions{})
	if err == nil {
		return fmt.Errorf("file with UUID %s already exists at path %s", uuid, path)
	}

	// 如果错误不是文件不存在，则返回错误
	if minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return err
	}

	return nil
}

// 添加日程
func addTaskHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var request TaskRequest
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

	newTask := &request.Task
	var filePath string

	for {
		// 生成UUID作为文件名
		uuid := uuid.New().String()
		newTask.Uuid = uuid
		filePath = prefix + uuid

		// 检查文件是否已存在
		err = CheckFileExists(prefix, uuid)
		if err == nil {
			// 如果文件不存在，则退出循环
			break
		}
	}

	// 将结构体编码为proto流
	newTaskData, err := gProto.Marshal(newTask)
	if err != nil {
		http.Error(w, "Failed to encode proto message", http.StatusInternalServerError)
		return
	}

	// 使用新的proto流替换已有文件的内容
	contentType := "application/octet-stream"
	_, err = minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		http.Error(w, "Failed to upload to MinIO", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Added successfully!",
		Task:    newTask,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// 修改日程
func updateTaskHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "PUT")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodPut {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var request TaskRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 将结构体编码为proto流
	newTaskData, err := gProto.Marshal(&request.Task)
	if err != nil {
		http.Error(w, "Failed to encode proto message", http.StatusInternalServerError)
		return
	}

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}
	filePath := prefix + request.Task.Uuid

	// 检查文件是否存在
	_, err = minioClient.StatObject(context.Background(), bucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		http.Error(w, "File not found in MinIO", http.StatusNotFound)
		return
	}

	// 使用新的proto流替换已有文件的内容
	contentType := "application/octet-stream"
	_, err = minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		http.Error(w, "Failed to upload to MinIO", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Updated successfully!",
		Task:    &request.Task,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// 复制日程
func copyTaskHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var request TaskRequest
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

	newTask := &request.Task
	var filePath string

	for {
		// 生成UUID作为文件名
		uuid := uuid.New().String()
		newTask.Uuid = uuid
		filePath = prefix + uuid

		// 检查文件是否已存在
		err = CheckFileExists(prefix, uuid)
		if err == nil {
			// 如果文件不存在，则退出循环
			break
		}
	}

	// 将结构体编码为proto流
	newTaskData, err := gProto.Marshal(newTask)
	if err != nil {
		http.Error(w, "Failed to encode proto message", http.StatusInternalServerError)
		return
	}

	contentType := "application/octet-stream"
	_, err = minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		http.Error(w, "Failed to copy task", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Copied successfully!",
		Task:    newTask,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// 删除日程
func deleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodDelete {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	type DeleteTaskRequest struct {
		Path  string `json:"path"`
		ComID string `json:"comID"`
		UUID  string `json:"uuid"`
	}

	var request DeleteTaskRequest
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

	err = minioClient.RemoveObject(context.Background(), bucketName, prefix+request.UUID, minio.RemoveObjectOptions{})
	if err != nil {
		http.Error(w, "Failed to delete file from MinIO", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Deleted successfully!",
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
	gzipWriter *gzip.Writer
}

func (g *gzipResponseWriter) WriteHeader(statusCode int) {
	// 设置 Gzip 响应头
	g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.WriteHeader(statusCode)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	// 写入 Gzip 压缩数据
	if g.gzipWriter != nil {
		return g.gzipWriter.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

type deflateResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (d *deflateResponseWriter) Write(b []byte) (int, error) {
	return d.Writer.Write(b)
}

func CompressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 检查是否为 WebSocket 握手请求
		if r.Header.Get("Upgrade") == "websocket" {
			next.ServeHTTP(w, r)
			return
		}

		// 检查请求头中支持的编码
		acceptEncoding := r.Header.Get("Accept-Encoding")
		if strings.Contains(acceptEncoding, "gzip") {
			// 使用 Gzip 压缩
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			gw := &gzipResponseWriter{
				Writer:         gz,
				ResponseWriter: w,
				gzipWriter:     gz,
			}
			next.ServeHTTP(gw, r)
		} else if strings.Contains(acceptEncoding, "deflate") {
			// 使用 Deflate 压缩
			var buf bytes.Buffer
			writer, err := flate.NewWriter(&buf, flate.BestCompression)
			if err != nil {
				http.Error(w, "Failed to create flate writer", http.StatusInternalServerError)
				return
			}
			defer writer.Close()

			w.Header().Set("Content-Encoding", "deflate")
			dw := &deflateResponseWriter{Writer: writer, ResponseWriter: w}
			next.ServeHTTP(dw, r)

			writer.Close()
			w.Write(buf.Bytes())
		} else {
			// 不支持压缩，直接处理请求
			next.ServeHTTP(w, r)
		}
	})
}

func main() {
	// 设置 WebSocket 处理器
	http.HandleFunc("/websocket", handleConnections)
	// 启动一个 goroutine 来处理消息广播
	go handleMessages()

	// 启动 UDP 组播侦听器
	go startUDPListener()

	http.HandleFunc("/devices", getDevicesHandler)            // 获取设备列表
	http.HandleFunc("/controlDevice", controlDeviceHandler)   // 控制设备
	http.HandleFunc("/addDeviceReply", addDeviceReplyHandler) // 添加设备回复

	initMinio() // 初始化MinIO
	http.HandleFunc("/taskList", getTaskListHandler)
	http.HandleFunc("/addTask", addTaskHandler)
	http.HandleFunc("/updateTask", updateTaskHandler)
	http.HandleFunc("/copyTask", copyTaskHandler)
	http.HandleFunc("/deleteTask", deleteTaskHandler)

	port := os.Getenv("MG_SOCKET_BRIDGE_PORT")
	if port == "" {
		port = "63001" // 默认端口
	}

	// 启动 HTTP 服务器，监听端口 63001`
	log.Println("HTTP server started on :" + port)
	err := http.ListenAndServe(":"+port, CompressionMiddleware(http.DefaultServeMux))
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
