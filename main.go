package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
	_ "github.com/lib/pq" // 确保这里有导入 PostgreSQL 驱动
	"github.com/minio/minio-go/v7"
)

var (
	pbMsgs      []PbMsg // 全局变量，用于存储接收到的设备信息
	mutex       sync.Mutex
	minioClient *minio.Client // MinIO 客户端
	bucketName  string        // 存储桶名称

	// 全局变量来存储所有连接的 WebSocket 客户端
	clients   = make(map[*websocket.Conn]bool)
	broadcast = make(chan []byte)
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	// messageHistory 是一个包含 sync.RWMutex 和 map 的结构体，用于存储消息主题和内容组合的键以及对应的时间戳。
	messageHistory = struct {
		sync.RWMutex
		m map[string]time.Time
	}{m: make(map[string]time.Time)}

	// 定义允许的 ProductName 列表
	allowedProductNames = map[string]struct{}{
		"NXT2204": {},
		"NXT3602": {},
		"NXT2102": {},
	}

	authDB   *sql.DB    // 数据库连接
	comIDs   []string   // 存储 com ID
	domainMu sync.Mutex // 用于保护对 domainIds 的并发访问

	thingsDB    *sql.DB       // 数据库连接
	mqttClients []mqtt.Client // 存储 MQTT 客户端
)

func main() {
	initThingsTable()
	initAuthTable()

	// 启动轮询协程
	go pollDomains()

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

	// 捕获系统信号以便安全关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// 等待接收到关闭信号
	<-quit
	log.Println("Shutting down server...")

	// 安全关闭数据库连接
	if err := authDB.Close(); err != nil {
		log.Fatalf("Error closing the auth database: %v", err)
	}
	if err := thingsDB.Close(); err != nil {
		log.Fatalf("Error closing the things database: %v", err)
	}
	log.Println("Database connection closed.")
}
