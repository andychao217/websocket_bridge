package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	proto "github.com/andychao217/websocket_bridge/proto"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	gProto "google.golang.org/protobuf/proto"
)

// 生成日程列表
func buildTaskList(client *minio.Client, bucket, prefix string) []*proto.Task {
	opts := minio.ListObjectsOptions{
		Recursive: false,
		Prefix:    prefix,
	}
	ctx := context.Background()

	objectCh := client.ListObjects(ctx, bucket, opts)

	var tasks []*proto.Task
	for object := range objectCh {
		if object.Err != nil {
			log.Println(object.Err)
			continue
		}

		objectName := object.Key
		obj, err := minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
		if err != nil {
			log.Println(err)
			continue
		}

		data, err := io.ReadAll(obj)
		obj.Close() // 确保在读取完数据后立即关闭对象
		if err != nil {
			log.Println(err)
			continue
		}

		var task proto.Task
		if err := gProto.Unmarshal(data, &task); err != nil {
			log.Println(err)
			continue
		}

		tasks = append(tasks, &task)
	}
	return tasks
}

// 获取日程列表
func getTaskListHandler(w http.ResponseWriter, r *http.Request) {
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

	type GetResourceListRequest struct {
		Path  string `json:"path"`
		ComID string `json:"comID"`
	}

	var request GetResourceListRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}

	// 构建日程列表
	taskList := buildTaskList(minioClient, bucketName, prefix)

	jsonData, err := json.MarshalIndent(taskList, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(jsonData)
}

// 检查日程文件是否已存在
func CheckFileExists(path, uuid string) error {
	// 构建文件路径
	filePath := path + uuid

	// 检查文件是否存在
	_, err := minioClient.StatObject(context.Background(), bucketName, filePath, minio.StatObjectOptions{})
	if err == nil {
		return fmt.Errorf("file with UUID %s already exists at path %s", uuid, path)
	}

	// 如果错误不是文件不存在，则返回错误
	if minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return err
	}

	return nil
}

// 添加日程
func addTaskHandler(w http.ResponseWriter, r *http.Request) {
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

	var request TaskRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}

	newTask := &request.Task
	var filePath string

	for {
		// 生成UUID作为文件名
		uuid := uuid.New().String()
		newTask.Uuid = uuid
		filePath = prefix + uuid

		// 检查文件是否已存在
		err = CheckFileExists(prefix, uuid)
		if err == nil {
			// 如果文件不存在，则退出循环
			break
		}
	}

	// 将结构体编码为proto流
	newTaskData, err := gProto.Marshal(newTask)
	if err != nil {
		http.Error(w, "Failed to encode proto message", http.StatusInternalServerError)
		return
	}

	// 使用新的proto流替换已有文件的内容
	contentType := "application/octet-stream"
	_, err = minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		http.Error(w, "Failed to upload to MinIO", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Added successfully!",
		Task:    newTask,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// 修改日程
func updateTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		handlePreflight(w, r)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "PUT")
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

	if r.Method != http.MethodPut {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var request TaskRequest
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

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}
	filePath := prefix + request.Task.Uuid

	// 检查文件是否存在
	_, err = minioClient.StatObject(context.Background(), bucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		http.Error(w, "File not found in MinIO", http.StatusNotFound)
		return
	}

	// 使用新的proto流替换已有文件的内容
	contentType := "application/octet-stream"
	_, err = minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		http.Error(w, "Failed to upload to MinIO", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Updated successfully!",
		Task:    &request.Task,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// 复制日程
func copyTaskHandler(w http.ResponseWriter, r *http.Request) {
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

	var request TaskRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}

	newTask := &request.Task
	var filePath string

	for {
		// 生成UUID作为文件名
		uuid := uuid.New().String()
		newTask.Uuid = uuid
		filePath = prefix + uuid

		// 检查文件是否已存在
		err = CheckFileExists(prefix, uuid)
		if err == nil {
			// 如果文件不存在，则退出循环
			break
		}
	}

	// 将结构体编码为proto流
	newTaskData, err := gProto.Marshal(newTask)
	if err != nil {
		http.Error(w, "Failed to encode proto message", http.StatusInternalServerError)
		return
	}

	contentType := "application/octet-stream"
	_, err = minioClient.PutObject(context.Background(), bucketName, filePath, bytes.NewReader(newTaskData), int64(len(newTaskData)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		http.Error(w, "Failed to copy task", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Copied successfully!",
		Task:    newTask,
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// 删除日程
func deleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		handlePreflight(w, r)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*") // 允许所有来源，或者指定具体的来源
	w.Header().Set("Access-Control-Allow-Methods", "DELETE")
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

	if r.Method != http.MethodDelete {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	type DeleteTaskRequest struct {
		Path  string `json:"path"`
		ComID string `json:"comID"`
		UUID  string `json:"uuid"`
	}

	var request DeleteTaskRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 假设comID用于构建bucket名称和路径
	var prefix string
	if request.Path == "" {
		prefix = request.ComID + "/task/"
	} else {
		prefix = request.Path
	}

	err = minioClient.RemoveObject(context.Background(), bucketName, prefix+request.UUID, minio.RemoveObjectOptions{})
	if err != nil {
		http.Error(w, "Failed to delete file from MinIO", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	w.WriteHeader(http.StatusOK)
	// 创建响应数据
	response := TaskResponse{
		Message: "Deleted successfully!",
	}
	// 将响应数据编码为JSON并写入响应
	err = json.NewEncoder(w).Encode(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
