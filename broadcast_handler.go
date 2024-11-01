package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	proto "github.com/andychao217/websocket_bridge/proto"
	"github.com/minio/minio-go/v7"
	gProto "google.golang.org/protobuf/proto"
)

// 新增、修改实时广播任务
func updateBroadcastTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		handlePreflight(w, r)
		return
	}

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

	var request BroadcastTaskRequest
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

	filePath := request.ComID + "/console/" + request.Username + "/" + request.Task.Uuid

	// 检查文件是否存在
	_, err = minioClient.StatObject(context.Background(), bucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			// 文件不存在，执行新增逻辑
			contentType := "application/octet-stream"
			_, err := minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
			if err != nil {
				http.Error(w, "Failed to upload to MinIO", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			response := BroadcastTaskResponse{
				Message: "Created successfully!",
				Task:    &request.Task,
			}
			err = json.NewEncoder(w).Encode(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			// 检查文件时出错
			http.Error(w, "File not found in MinIO", http.StatusNotFound)
			return
		}
	} else {
		// 文件存在，执行更新逻辑
		contentType := "application/octet-stream"
		_, err := minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
		if err != nil {
			http.Error(w, "Failed to upload to MinIO", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		response := BroadcastTaskResponse{
			Message: "Updated successfully!",
			Task:    &request.Task,
		}
		err = json.NewEncoder(w).Encode(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

// 查询实时广播任务
func getBroadcastTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		handlePreflight(w, r)
		return
	}

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

	var request BroadcastTaskRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	filePath := request.ComID + "/console/" + request.Username + "/" + request.Uuid

	// 检查文件是否存在
	broadcastTaskFile, err := minioClient.GetObject(context.Background(), bucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	broadcastTaskData, err := io.ReadAll(broadcastTaskFile)
	broadcastTaskFile.Close() // 确保在读取完数据后立即关闭对象
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var broadcastTask proto.SoundConsoleScene
	if err := gProto.Unmarshal(broadcastTaskData, &broadcastTask); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := BroadcastTaskResponse{
		Message: "Get broadcast task successfully!",
		Task:    &broadcastTask,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
