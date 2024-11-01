package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"time"

	proto "github.com/andychao217/websocket_bridge/proto"
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
