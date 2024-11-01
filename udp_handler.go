package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	proto "github.com/andychao217/websocket_bridge/proto"
	gProto "google.golang.org/protobuf/proto"
)

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

		// 过滤不在允许列表中的 ProductName
		if !isProductAllowed(temp.ProductName) {
			// fmt.Printf("产品名称 '%s' 不在允许的列表中，跳过处理。\n", temp.ProductName)
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
	if r.Method == http.MethodOptions {
		handlePreflight(w, r)
		return
	}

	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "GET")
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
