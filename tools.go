package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	proto "github.com/andychao217/websocket_bridge/proto"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	gProto "google.golang.org/protobuf/proto"
)

// 生成mqtt clientID字符串
func generateClientID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	clientID := make([]byte, 10)
	for i := range clientID {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		clientID[i] = charset[num.Int64()]
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10) // 将时间戳转换为字符串
	return string(clientID) + "_" + timestamp + "_socketbridge"
}

// clearPbMsgs 清空 pbMsgs
func clearPbMsgs() {
	mutex.Lock()
	defer mutex.Unlock()
	pbMsgs = nil
}

// isProductAllowed 检查 ProductName 是否在允许的列表中
func isProductAllowed(productName string) bool {
	_, exists := allowedProductNames[productName]
	return exists
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

// 创建请求数据
func createRequestData(params *struct {
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
		reqData = &proto.TaskSync{
			Username: params.Username,
			Type:     taskSyncType,
			TaskUuid: params.Uuid,
		}
		pbMsgId = 312
	case "soundConsleTaskControl":
		reqData = &proto.SoundConsoleTaskControl{
			Username:   params.Username,
			Uuid:       params.Uuid,
			Command:    params.SoundConsoleTaskControl.Command,
			Song:       params.SoundConsoleTaskControl.Song,
			Position:   params.SoundConsoleTaskControl.Position,
			Volume:     params.SoundConsoleTaskControl.Volume,
			VolumeType: params.SoundConsoleTaskControl.VolumeType,
		}
		pbMsgId = 275
	case "taskStart":
		reqData = &proto.TaskStart{Username: params.Username, Task: params.Task}
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
		reqData = &proto.DevicePowerSet{
			Username:   params.Username,
			Out_1Power: params.DevicePowerPack.Out_1Power,
			Out_2Power: params.DevicePowerPack.Out_2Power,
			Out_3Power: params.DevicePowerPack.Out_3Power,
			Out_4Power: params.DevicePowerPack.Out_4Power,
		}
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
	case "deviceUpgrade":
		reqData = &proto.DeviceUpgrade{FirmwareUrl: params.FirmwareUrl}
		pbMsgId = 306
	default:
		return nil, 0, fmt.Errorf("invalid control type: %s", params.ControlType)
	}

	return reqData, pbMsgId, nil
}

// 测试 MQTT 消息的 Handler
func testMqttHandler(w http.ResponseWriter, r *http.Request) {
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

	// 使用 goroutine 异步发送 MQTT 消息
	go func() {
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
			AddBroker(fmt.Sprintf("tcp://%s:1883", "120.78.0.143")).
			SetClientID(clientID).
			SetUsername(nameAndPass).
			SetPassword(nameAndPass).
			SetAutoReconnect(true).
			SetConnectTimeout(300 * time.Second) // 增加连接超时

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

		for i := 0; i < 100_000; i++ {
			// 发送 MQTT 消息
			token := client.Publish(strings.Replace(topic, " ", "", -1), 0, false, buf)
			fmt.Printf("MQTT message sent: %v\n", i)
			token.Wait()
			if token.Error() != nil {
				fmt.Printf("Error sending MQTT message: %v\n", token.Error())
				// 可以根据需求记录错误，或进行其他处理
			}
		}
	}()

	// 立即返回成功响应
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "success"}`))
}
