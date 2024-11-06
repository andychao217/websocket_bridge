package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	proto "github.com/andychao217/websocket_bridge/proto"
)

// 初始化auth数据库连接
func initAuthTable() {
	// 使用 localhost 和映射的端口 6004 连接 PostgreSQL
	// 数据库连接字符串
	connStr := "host=localhost port=6004 user=magistrala password=magistrala dbname=auth sslmode=disable"
	var err error
	authDB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Error connecting to the auth database: %v", err)
	}

	// 可选：验证连接
	if err := authDB.Ping(); err != nil {
		log.Fatalf("Error pinging the auth database: %v", err)
	}
}

// 查询 domains 表并更新 domainIds
func queryDomains() {
	rows, err := authDB.Query("SELECT metadata->>'comID' FROM domains")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var newComIDs []string
	for rows.Next() {
		var comId sql.NullString // 使用 sql.NullString 类型
		if err := rows.Scan(&comId); err != nil {
			log.Fatal(err)
		}

		// 处理可能的 NULL 值
		if comId.Valid {
			// 如果 id 有效（不为 NULL），则添加到 newComIDs
			newComIDs = append(newComIDs, comId.String)
		}
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	// 更新 domainIds，并打印变化
	domainMu.Lock()
	if len(newComIDs) != len(comIDs) {
		if len(mqttClients) > 0 {
			for _, client := range mqttClients {
				disconnectFromMQTT(client)
			}
		}
		if len(newComIDs) > 0 {
			for _, comID := range newComIDs {
				mqttClients = append(mqttClients, subscribeToMQTTTopic("localhost", "platform"+comID, "channels/"+comID+"/messages/#"))
			}
		}
	}
	comIDs = newComIDs
	domainMu.Unlock()
}

// 轮询auth功能，每5秒查询一次
func pollDomains() {
	for {
		queryDomains()
		time.Sleep(5 * time.Second) // 每5秒轮询一次
	}
}

// 初始化things数据库连接
func initThingsTable() {
	// 使用 localhost 和映射的端口 6006 连接 PostgreSQL
	// 数据库连接字符串
	connStr := "host=localhost port=6006 user=magistrala password=magistrala dbname=things sslmode=disable"
	var err error
	thingsDB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Error connecting to the things database: %v", err)
	}

	// 可选：验证连接
	if err := thingsDB.Ping(); err != nil {
		log.Fatalf("Error pinging the things database: %v", err)
	}
}

// 从things数据库查询设备数据
func getThingClientFromDB(db *sql.DB, identity string) (Client, error) {
	query := `SELECT id, name, tags, COALESCE(domain_id, '') AS domain_id, identity, secret, metadata, created_at, updated_at, updated_by, status
              FROM clients WHERE identity = $1`
	row := db.QueryRow(query, identity)

	var client Client
	var tagsJSON sql.NullString // 中间变量来接收 tags 列的值
	var metadataJSON sql.NullString

	// 扫描结果到变量
	if err := row.Scan(
		&client.ID,
		&client.Name,
		&tagsJSON,
		&client.DomainID,
		&client.Identity,
		&client.Secret,
		&metadataJSON,
		&client.CreatedAt,
		&client.UpdatedAt,
		&client.UpdatedBy,
		&client.Status,
	); err != nil {
		return Client{}, err // 处理扫描错误
	}

	// 解析 metadata JSON 数据
	if metadataJSON.Valid {
		if err := json.Unmarshal([]byte(metadataJSON.String), &client.Metadata); err != nil {
			return Client{}, err // 处理 JSON 解析错误
		}
	} else {
		client.Metadata = make(map[string]interface{}) // 初始化为一个空的 MAP
	}

	// 处理 tags 数据
	if tagsJSON.Valid {
		if err := json.Unmarshal([]byte(tagsJSON.String), &client.Tags); err != nil {
			return Client{}, err // 处理 JSON 解析错误
		}
	} else {
		client.Tags = []string{} // 将其初始化为空切片
	}

	return client, nil
}

// 更新设备在线状态
func updateClientConnectionStatus(identity, onlineStatus string) {
	thing, err := getThingClientFromDB(thingsDB, identity)
	if err != nil {
		log.Fatal(err)
	}

	if thing.ID != "" && !strings.Contains(thing.Name, "Platform") {
		thing.Metadata["is_online"] = onlineStatus
		now := time.Now()
		thing.UpdatedAt = &now
		_ = updateThingDB(thingsDB, thing)

		// out_channel 大于1, 且is_channel等于0时，说明是多通道设备，需要把多通道都同时修改onlineStatus
		// 从 Metadata 中获取 "out_channel" 的值，并进行类型断言
		outChannelStr, ok := thing.Metadata["out_channel"].(string)
		if ok {
			outChannelInt, err := strconv.Atoi(outChannelStr)
			if err != nil {
				fmt.Println("Failed to convert out_channel to int:", err)
			} else {
				if outChannelInt > 1 {
					is_channel, ok := thing.Metadata["is_channel"].(string)
					if ok {
						if is_channel == "0" {
							for i := 2; i <= outChannelInt; i++ {
								newThing, err := getThingClientFromDB(thingsDB, identity+"_"+strconv.Itoa(i))
								if err != nil {
									continue // 如果检索失败，继续下一个循环
								}
								if newThing.ID != "" {
									newThing.Metadata["is_online"] = onlineStatus
									newThing.UpdatedAt = &now
									_ = updateThingDB(thingsDB, newThing)
								}
							}
						}
					}
				}
			}
		}
	}

}

// 修改设备信息
func updateClientInfo(newDeviceInfo *proto.DeviceInfo) {
	thing, err := getThingClientFromDB(thingsDB, newDeviceInfo.DeviceName)
	if err != nil {
		log.Fatal(err)
	}

	if thing.ID != "" && !strings.Contains(thing.Name, "Platform") {
		if info, exists := thing.Metadata["info"]; !exists {
			// 如果不存在，调用 updateInfo()
			updateInfo(thing, newDeviceInfo)
		} else {
			// 将 info 转换为 JSON 字符串
			infoJson, err := json.Marshal(info)
			if err != nil {
				return
			}

			// 将 JSON 字符串解码为 *proto.DeviceInfo
			var oldDeviceInfo proto.DeviceInfo
			if err := json.Unmarshal(infoJson, &oldDeviceInfo); err != nil {
				fmt.Println("Failed to unmarshal info to *proto.DeviceInfo:", err)
				return
			}

			oldJson, err := json.Marshal(oldDeviceInfo)
			if err != nil {
				return
			}

			newJson, err := json.Marshal(newDeviceInfo)
			if err != nil {
				return
			}

			fmt.Printf("oldJson 12345678: %v\n", string(oldJson))
			fmt.Printf("newJson 12345678: %v\n", string(newJson))

			if string(oldJson) != string(newJson) {
				updateInfo(thing, newDeviceInfo)
			}
		}
	}
}

// 更新设备信息
func updateInfo(thing Client, newDeviceInfo *proto.DeviceInfo) {
	thing.Metadata["info"] = newDeviceInfo
	now := time.Now()
	thing.UpdatedAt = &now
	_ = updateThingDB(thingsDB, thing)

	// out_channel 大于1, 且is_channel等于0时，说明是多通道设备，需要把多通道都同时修改onlineStatus
	// 从 Metadata 中获取 "out_channel" 的值，并进行类型断言
	outChannelStr, ok := thing.Metadata["out_channel"].(string)
	if ok {
		outChannelInt, err := strconv.Atoi(outChannelStr)
		if err != nil {
			fmt.Println("Failed to convert out_channel to int:", err)
		} else {
			if outChannelInt > 1 {
				is_channel, ok := thing.Metadata["is_channel"].(string)
				if ok {
					if is_channel == "0" {
						for i := 2; i <= outChannelInt; i++ {
							newThing, err := getThingClientFromDB(thingsDB, thing.Identity+"_"+strconv.Itoa(i))
							if err != nil {
								continue // 如果检索失败，继续下一个循环
							}
							if newThing.ID != "" {
								newThing.Metadata["info"] = newDeviceInfo
								newThing.UpdatedAt = &now
								outChannelData := newDeviceInfo.OutChannel
								channels := outChannelData.Channel
								if len(channels) > 0 {
									if i-1 < len(channels) {
										channelInfo := channels[i-1]
										channel_aliase := channelInfo.Aliase
										device_aliase := newDeviceInfo.DeviceAliase
										name := device_aliase + "_" + channel_aliase
										newThing.Metadata["aliase"] = name
										newThing.Name = name
									}
									newThing.Metadata["out_channel_array"] = channels
								}
								_ = updateThingDB(thingsDB, newThing)
							}
						}
					}
				}
			}
		}
	}
}

// 更新数据库中的客户端信息
func updateThingDB(db *sql.DB, thing Client) error {
	// 将 Metadata 转换为 JSON 字符串
	metadataJSON, err := json.Marshal(thing.Metadata)
	if err != nil {
		return err // 处理 JSON 序列化错误
	}
	// 更新 metadata
	query := `UPDATE clients SET metadata = $1 WHERE identity = $2`
	_, err = db.Exec(query, metadataJSON, thing.Identity)
	if err != nil {
		return err // 处理更新错误
	}
	return nil
}
