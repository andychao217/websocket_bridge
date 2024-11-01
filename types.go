package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"sync"
	"time"

	proto "github.com/andychao217/websocket_bridge/proto"
	"github.com/gorilla/websocket"
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
)

// PbMsg 代表设备信息的结构体
type PbMsg struct {
	Timestamp   time.Time `json:"timestamp"`
	Message     string    `json:"message"`
	MessageType string    `json:"message_type"`
}

// WebSocket 消息结构
type WebSocketMessage struct {
	Topics      []string `json:"topics"`
	Host        string   `json:"host"`
	ThingSecret string   `json:"thingSecret"`
	Message     string   `json:"message"`
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

// 实时广播任务请求结构体-添加、更新
type BroadcastTaskRequest struct {
	ComID    string                  `json:"comID"`
	Username string                  `json:"username"`
	Uuid     string                  `json:"uuid"`
	Task     proto.SoundConsoleScene `json:"task"`
}

// 实时广播任务响应结构体
type BroadcastTaskResponse struct {
	Message string                   `json:"message"`
	Task    *proto.SoundConsoleScene `json:"task"`
}

// gzip压缩响应
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
	gzipWriter *gzip.Writer
}

// deflate解压响应
type deflateResponseWriter struct {
	io.Writer
	http.ResponseWriter
}
