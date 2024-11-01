package main

import (
	"log"
	"net/http"
	"os"
)

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
	// http.HandleFunc("/testMqtt", testMqttHandler)

	initMinio() // 初始化MinIO
	http.HandleFunc("/taskList", getTaskListHandler)
	http.HandleFunc("/addTask", addTaskHandler)
	http.HandleFunc("/updateTask", updateTaskHandler)
	http.HandleFunc("/copyTask", copyTaskHandler)
	http.HandleFunc("/deleteTask", deleteTaskHandler)
	http.HandleFunc("/updateBroadcastTask", updateBroadcastTaskHandler)
	http.HandleFunc("/getBroadcastTask", getBroadcastTaskHandler)

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
