package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	proto "github.com/andychao217/websocket_bridge/proto"
	"github.com/gorilla/websocket"
	gProto "google.golang.org/protobuf/proto"
)

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
}

// 1. 定义消息元数据结构：整合解析器和消息对象创建函数
type msgMeta struct {
	parser     func([]byte, interface{}) error // Proto 解析函数
	newMsgFunc func() interface{}              // 创建对应消息类型的对象（如 &proto.TaskStart{}）
}

// 原有的结构体定义（保持不变）
type PayloadData struct {
	Data    interface{} `json:"data"`
	Source  string      `json:"source"`
	MsgName string      `json:"msgName"`
}

// 2. 初始化消息元数据映射（替代原有的 msgParsers 和 switch）
var msgMetaMap = map[string]msgMeta{
	"TASK_START": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStart)) },
		newMsgFunc: func() interface{} { return &proto.TaskStart{} },
	},
	"TASK_START_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStartReply)) },
		newMsgFunc: func() interface{} { return &proto.TaskStartReply{} },
	},
	"TASK_STOP": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStop)) },
		newMsgFunc: func() interface{} { return &proto.TaskStop{} },
	},
	"TASK_STOP_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStopReply)) },
		newMsgFunc: func() interface{} { return &proto.TaskStopReply{} },
	},
	"TASK_STATUS_GET": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStatusGet)) },
		newMsgFunc: func() interface{} { return &proto.TaskStatusGet{} },
	},
	"TASK_STATUS_GET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStatusGetReply)) },
		newMsgFunc: func() interface{} { return &proto.TaskStatusGetReply{} },
	},
	"TASK_SYNC_STATUS_GET": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskSyncStatusGet)) },
		newMsgFunc: func() interface{} { return &proto.TaskSyncStatusGet{} },
	},
	"TASK_SYNC_STATUS_GET_REPLY": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.TaskSyncStatusGetReply))
		},
		newMsgFunc: func() interface{} { return &proto.TaskSyncStatusGetReply{} },
	},
	"SOUND_CONSOLE_TASK_CONTROL_REPLY": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.SoundConsoleTaskControlReply))
		},
		newMsgFunc: func() interface{} { return &proto.SoundConsoleTaskControlReply{} },
	},
	"SOUND_CONSOLE_TASK_FEEDBACK": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.SoundConsoleTaskFeedback))
		},
		newMsgFunc: func() interface{} { return &proto.SoundConsoleTaskFeedback{} },
	},
	"GET_LOG_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.GetLogReply)) },
		newMsgFunc: func() interface{} { return &proto.GetLogReply{} },
	},
	"DEVICE_LOGIN": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceLogin)) },
		newMsgFunc: func() interface{} { return &proto.DeviceLogin{} },
	},
	"DEVICE_INFO_GET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceInfoGetReply)) },
		newMsgFunc: func() interface{} { return &proto.DeviceInfoGetReply{} },
	},
	"DEVICE_INFO_UPDATE": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceInfoUpdate)) },
		newMsgFunc: func() interface{} { return &proto.DeviceInfoUpdate{} },
	},
	"DEVICE_RESTORE_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceRestoreReply)) },
		newMsgFunc: func() interface{} { return &proto.DeviceRestoreReply{} },
	},
	"DEVICE_ALIASE_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceAliaseSetReply)) },
		newMsgFunc: func() interface{} { return &proto.DeviceAliaseSetReply{} },
	},
	"LED_CFG_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.LedCfgSetReply)) },
		newMsgFunc: func() interface{} { return &proto.LedCfgSetReply{} },
	},
	"STEREO_CFG_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.StereoCfgSetReply)) },
		newMsgFunc: func() interface{} { return &proto.StereoCfgSetReply{} },
	},
	"OUT_CHANNEL_EDIT_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.OutChannelEditReply)) },
		newMsgFunc: func() interface{} { return &proto.OutChannelEditReply{} },
	},
	"IN_CHANNEL_EDIT_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.InChannelEditReply)) },
		newMsgFunc: func() interface{} { return &proto.InChannelEditReply{} },
	},
	"BLUETOOTH_CFG_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.BluetoothCfgSetReply)) },
		newMsgFunc: func() interface{} { return &proto.BluetoothCfgSetReply{} },
	},
	"SPEECH_CFG_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.SpeechCfgSetReply)) },
		newMsgFunc: func() interface{} { return &proto.SpeechCfgSetReply{} },
	},
	"BLUETOOTH_WHITELIST_ADD_REPLY": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.BluetoothWhitelistAddReply))
		},
		newMsgFunc: func() interface{} { return &proto.BluetoothWhitelistAddReply{} },
	},
	"BLUETOOTH_WHITELIST_DELETE_REPLY": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.BluetoothWhitelistDeleteReply))
		},
		newMsgFunc: func() interface{} { return &proto.BluetoothWhitelistDeleteReply{} },
	},
	"AMP_CHECK_CFG_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.AmpCheckCfgSetReply)) },
		newMsgFunc: func() interface{} { return &proto.AmpCheckCfgSetReply{} },
	},
	"AUDIO_MATRIX_CFG_SET_REPLY": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.AudioMatrixCfgSetReply))
		},
		newMsgFunc: func() interface{} { return &proto.AudioMatrixCfgSetReply{} },
	},
	"RADIO_FREQ_GET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.RadioFreqGetReply)) },
		newMsgFunc: func() interface{} { return &proto.RadioFreqGetReply{} },
	},
	"RADIO_FREQ_ADD_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.RadioFreqAddReply)) },
		newMsgFunc: func() interface{} { return &proto.RadioFreqAddReply{} },
	},
	"RADIO_FREQ_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.RadioFreqSetReply)) },
		newMsgFunc: func() interface{} { return &proto.RadioFreqSetReply{} },
	},
	"RADIO_FREQ_DELETE_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.RadioFreqDeleteReply)) },
		newMsgFunc: func() interface{} { return &proto.RadioFreqDeleteReply{} },
	},
	"U_CHANNEL_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.UChannelSetReply)) },
		newMsgFunc: func() interface{} { return &proto.UChannelSetReply{} },
	},
	"EQ_CFG_SET_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.EqCfgSetReply)) },
		newMsgFunc: func() interface{} { return &proto.EqCfgSetReply{} },
	},
	"SPEAKER_VOLUME_SET_REPLY": {
		parser: func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.SpeakerVolumeSetReply))
		},
		newMsgFunc: func() interface{} { return &proto.SpeakerVolumeSetReply{} },
	},
	"DEVICE_UPGRADE_REPLY": {
		parser:     func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.DeviceUpgradeReply)) },
		newMsgFunc: func() interface{} { return &proto.DeviceUpgradeReply{} },
	},
}

// 2. 提取公共消息解析函数：解析消息并返回解析后的对象
func parseMessage(msgIdName string, data []byte) (interface{}, error) {
	meta, exists := msgMetaMap[msgIdName]
	if !exists {
		log.Printf("未知的消息类型: %s", msgIdName)
		return nil, fmt.Errorf("未知的消息类型: %s", msgIdName)
	}

	msgData := meta.newMsgFunc()
	if err := meta.parser(data, msgData); err != nil {
		log.Printf("解析消息 %s 失败: %v", msgIdName, err)
		return nil, fmt.Errorf("解析消息 %s 失败: %v", msgIdName, err)
	}
	return msgData, nil
}

// 3. 提取数据库更新逻辑为子函数
func handleDBUpdate(msgIdName string, msgData interface{}) {
	switch msgIdName {
	case "DEVICE_LOGIN":
		if loginData, ok := msgData.(*proto.DeviceLogin); ok {
			deviceIdentity := loginData.DeviceName
			loginStatus := "0"
			if loginData.Login {
				loginStatus = "1"
			}
			updateClientConnectionStatus(deviceIdentity, loginStatus)
		}
	case "DEVICE_INFO_UPDATE":
		if deviceData, ok := msgData.(*proto.DeviceInfoUpdate); ok && deviceData.Info != nil {
			updateClientInfo(deviceData.Info)
		}
	case "DEVICE_INFO_GET_REPLY":
		if deviceData, ok := msgData.(*proto.DeviceInfoGetReply); ok && deviceData.Info != nil {
			updateClientInfo(deviceData.Info)
		}
		// ... 其他需要数据库更新的消息类型（如果有）
	}
}

// 4. 提取 WebSocket 广播逻辑为子函数
func handleBroadcast(unmarshaledData PayloadData) {
	jsonBytes, err := json.Marshal(unmarshaledData)
	if err != nil {
		log.Printf("转换为 JSON 失败: %v", err)
		return
	}

	// 有客户端时才广播
	if len(clients) > 0 {
		for client := range clients {
			if err := client.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
				log.Printf("WebSocket 发送失败: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

// 5. 优化后的主函数
func handleMessages() {
	for {
		select {
		case payload := <-dbUpdateChan:
			// 解析 PbMsg 基础结构
			var receivedMsg proto.PbMsg
			if err := gProto.Unmarshal(payload, &receivedMsg); err != nil {
				log.Printf("解析 PbMsg 失败: %v", err)
				continue
			}
			msgIdName := proto.MsgId_name[int32(receivedMsg.Id)]
			data := receivedMsg.Data

			// 调用公共解析函数
			msgData, err := parseMessage(msgIdName, data)
			if err != nil {
				continue // 解析失败直接跳过
			}

			// 处理数据库更新
			handleDBUpdate(msgIdName, msgData)

		case payload := <-broadcast:
			// 解析 PbMsg 基础结构
			var receivedMsg proto.PbMsg
			if err := gProto.Unmarshal(payload, &receivedMsg); err != nil {
				log.Printf("解析 PbMsg 失败: %v", err)
				continue
			}
			msgIdName := proto.MsgId_name[int32(receivedMsg.Id)]
			data := receivedMsg.Data

			// 调用公共解析函数
			msgData, err := parseMessage(msgIdName, data)
			if err != nil {
				continue
			}

			// 准备广播数据
			unmarshaledData := PayloadData{
				MsgName: msgIdName,
				Source:  receivedMsg.Source,
				Data:    msgData,
			}

			// 处理 WebSocket 广播
			handleBroadcast(unmarshaledData)
		}
	}
}
