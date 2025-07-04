package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	proto "github.com/andychao217/websocket_bridge/proto"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	gProto "google.golang.org/protobuf/proto"
)

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
		messageHistory.Lock()
		messageHistory.m[msg.Topic()+string(msg.Payload())] = time.Now()
		messageHistory.Unlock()
		broadcast <- msg.Payload()
	}

	// 2. 无条件发送到数据库更新通道（所有消息都处理，不去重）
	dbUpdateChan <- msg.Payload()
}

// 封装的 MQTT 连接和订阅函数
func subscribeToMQTTTopic(broker, thingSecret, topic string) mqtt.Client {
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
		return nil
	}

	if token := client.Subscribe(topic, 0, nil); token.Wait() && token.Error() != nil {
		fmt.Printf("Failed to subscribe to MQTT topic: %v\n", token.Error())
		return nil
	} else {
		fmt.Printf("Successfully subscribed to MQTT topic: %v\n", topic)
	}

	return client // 返回 MQTT 客户端
}

// 断开 MQTT 连接的函数
func disconnectFromMQTT(client mqtt.Client) {
	if client != nil {
		client.Disconnect(250) // 250ms 超时
		fmt.Println("Disconnected from MQTT broker.")
	}
}

// 给设备发送指令
func controlDeviceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		handlePreflight(w, r)
		return
	}
	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Content-Type", "application/json")

	// 验证请求头中是否有 Authorization
	// authHeader := r.Header.Get("Authorization")
	// if authHeader == "" {
	// 	// 如果没有 Authorization 头，则返回 401 未授权错误
	// 	http.Error(w, "Authorization header missing", http.StatusUnauthorized)
	// 	return
	// }

	w.WriteHeader(http.StatusOK)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// 这里可以添加处理 POST 请求的逻辑
	var params struct {
		ChannelID               string                         `json:"channelID"`
		ThingIdentity           string                         `json:"thingIdentity"`
		Host                    string                         `json:"host"`
		ComID                   string                         `json:"comID"`
		ControlType             string                         `json:"controlType"`
		Uuid                    string                         `json:"uuid"`
		Task                    *proto.Task                    `json:"task"`
		Username                string                         `json:"username"`
		DeviceInfo              *proto.DeviceInfo              `json:"deviceInfo"`
		ChannelAttr             *proto.ChannelAttr             `json:"channelAttr"`
		SpeechCfg               *proto.SpeechCfg               `json:"speechCfg"`
		DevicePowerPack         *proto.DevicePowerPack         `json:"devicePowerPack"`
		SoundConsoleTaskControl *proto.SoundConsoleTaskControl `json:"soundConsoleTaskControl"`
		UChannel                int32                          `json:"uChannel"`
		EqCfg                   *proto.EqCfg                   `json:"eqCfg"`
		SpeakerVolume           *proto.SpeakerVolume           `json:"speakerVolume"`
		AudioMatrix             *proto.AudioMatrix             `json:"audioMatrix"`
		RadioFreq               *proto.RadioFreq               `json:"radioFreq"`
		BluetoothCfg            *proto.BluetoothCfg            `json:"bluetoothCfg"`
		BluetoothWhitelist      *proto.BluetoothWhitelist      `json:"bluetoothWhitelist"`
		FirmwareUrl             string                         `json:"firmwareUrl"`
		Enabled                 bool                           `json:"enabled"`
	}

	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		http.Error(w, "Error decoding request body", http.StatusBadRequest)
		return
	}

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
		ControlType             string
		Username                string
		Task                    *proto.Task // 修改为指针类型
		Uuid                    string
		ComID                   string
		DeviceInfo              *proto.DeviceInfo
		ChannelAttr             *proto.ChannelAttr
		SpeechCfg               *proto.SpeechCfg
		DevicePowerPack         *proto.DevicePowerPack
		SoundConsoleTaskControl *proto.SoundConsoleTaskControl
		UChannel                int32
		EqCfg                   *proto.EqCfg
		SpeakerVolume           *proto.SpeakerVolume
		AudioMatrix             *proto.AudioMatrix
		RadioFreq               *proto.RadioFreq
		BluetoothCfg            *proto.BluetoothCfg
		BluetoothWhitelist      *proto.BluetoothWhitelist
		FirmwareUrl             string
		Enabled                 bool
	}{
		ControlType:             params.ControlType,
		Username:                params.Username,
		Task:                    params.Task, // 直接使用指针
		Uuid:                    params.Uuid,
		ComID:                   params.ComID,
		DeviceInfo:              params.DeviceInfo,
		ChannelAttr:             params.ChannelAttr,
		SpeechCfg:               params.SpeechCfg,
		DevicePowerPack:         params.DevicePowerPack,
		SoundConsoleTaskControl: params.SoundConsoleTaskControl,
		UChannel:                params.UChannel,
		EqCfg:                   params.EqCfg,
		SpeakerVolume:           params.SpeakerVolume,
		AudioMatrix:             params.AudioMatrix,
		RadioFreq:               params.RadioFreq,
		BluetoothCfg:            params.BluetoothCfg,
		BluetoothWhitelist:      params.BluetoothWhitelist,
		FirmwareUrl:             params.FirmwareUrl,
		Enabled:                 params.Enabled,
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
