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
		"TASK_START_REPLY":      func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStartReply)) },
		"TASK_STOP":             func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStop)) },
		"TASK_STOP_REPLY":       func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStopReply)) },
		"TASK_STATUS_GET":       func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStatusGet)) },
		"TASK_STATUS_GET_REPLY": func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskStatusGetReply)) },
		"TASK_SYNC_STATUS_GET":  func(data []byte, v interface{}) error { return gProto.Unmarshal(data, v.(*proto.TaskSyncStatusGet)) },
		"TASK_SYNC_STATUS_GET_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.TaskSyncStatusGetReply))
		},
		"SOUND_CONSOLE_TASK_CONTROL_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.SoundConsoleTaskControlReply))
		},
		"SOUND_CONSOLE_TASK_FEEDBACK": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.SoundConsoleTaskFeedback))
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
		"DEVICE_UPGRADE_REPLY": func(data []byte, v interface{}) error {
			return gProto.Unmarshal(data, v.(*proto.DeviceUpgradeReply))
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
			case "TASK_START_REPLY":
				msgData = &proto.TaskStartReply{}
			case "TASK_STOP":
				msgData = &proto.TaskStop{}
			case "TASK_STOP_REPLY":
				msgData = &proto.TaskStopReply{}
			case "TASK_STATUS_GET":
				msgData = &proto.TaskStatusGet{}
			case "TASK_STATUS_GET_REPLY":
				msgData = &proto.TaskStatusGetReply{}
			case "TASK_SYNC_STATUS_GET":
				msgData = &proto.TaskSyncStatusGet{}
			case "TASK_SYNC_STATUS_GET_REPLY":
				msgData = &proto.TaskSyncStatusGetReply{}
			case "SOUND_CONSOLE_TASK_CONTROL_REPLY":
				msgData = &proto.SoundConsoleTaskControlReply{}
			case "SOUND_CONSOLE_TASK_FEEDBACK":
				msgData = &proto.SoundConsoleTaskFeedback{}
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
			case "DEVICE_UPGRADE_REPLY":
				msgData = &proto.DeviceUpgradeReply{}
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
