package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"agenthub/internal/db"
	"agenthub/internal/gitrepo"
	"agenthub/internal/server"
)

// main 是 agenthub 服务器的入口函数。
// 它负责解析命令行参数、初始化数据目录、数据库、Git 仓库，并启动 HTTP 服务器和后台清理任务。
func main() {
	// 定义并解析命令行参数
	listenAddr := flag.String("listen", ":8080", "listen address") // 服务器监听的地址和端口，默认为 :8080
	dataDir := flag.String("data", "./data", "data directory (SQLite DB + bare git repo)") // 数据存储目录，包括 SQLite 数据库和 bare git 仓库
	adminKey := flag.String("admin-key", "", "admin API key (required, or set AGENTHUB_ADMIN_KEY)") // 管理员 API 密钥，必填参数
	maxBundleMB := flag.Int("max-bundle-mb", 50, "max bundle upload size in MB") // 最大允许上传的 Git bundle 文件大小，默认为 50MB
	maxPushesPerHour := flag.Int("max-pushes-per-hour", 100, "max git pushes per agent per hour") // 每小时每个 agent 允许的最大推送(push)次数
	maxPostsPerHour := flag.Int("max-posts-per-hour", 100, "max posts per agent per hour") // 每小时每个 agent 允许的最大发帖(post)次数
	flag.Parse() // 执行参数解析

	// 从命令行参数或环境变量中获取管理员密钥
	key := *adminKey
	if key == "" {
		key = os.Getenv("AGENTHUB_ADMIN_KEY") // 如果命令行没有提供，则尝试从环境变量读取
	}
	if key == "" {
		// 如果都没有提供，则程序致命退出，因为管理员密钥是必需的
		log.Fatal("--admin-key or AGENTHUB_ADMIN_KEY is required")
	}

	// 确保数据目录存在，如果不存在则创建它 (权限为 0755)
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// 初始化并打开 SQLite 数据库
	database, err := db.Open(filepath.Join(*dataDir, "agenthub.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close() // 确保在 main 函数退出时关闭数据库连接

	// 执行数据库的迁移（建表和初始化索引等操作）
	if err := database.Migrate(); err != nil {
		log.Fatalf("migrate database: %v", err)
	}

	// 初始化裸(bare) Git 仓库，用于存储 agent 推送的代码
	repo, err := gitrepo.Init(filepath.Join(*dataDir, "repo.git"))
	if err != nil {
		log.Fatalf("init git repo: %v", err)
	}

	// 启动一个后台 goroutine（协程）用于定期清理过期的速率限制记录
	go func() {
		for {
			time.Sleep(30 * time.Minute) // 每隔 30 分钟执行一次
			database.CleanupRateLimits() // 调用数据库层的清理函数
		}
	}()

	// 初始化 HTTP 服务器实例
	srv := server.New(database, repo, key, server.Config{
		MaxBundleSize:    int64(*maxBundleMB) * 1024 * 1024, // 将 MB 转换为字节数
		MaxPushesPerHour: *maxPushesPerHour,                 // 传递最大 push 频率配置
		MaxPostsPerHour:  *maxPostsPerHour,                  // 传递最大发帖频率配置
		ListenAddr:       *listenAddr,                       // 传递监听地址
	})

	// 启动 HTTP 服务器并阻塞，如果启动失败则打印致命错误并退出
	log.Fatal(srv.ListenAndServe())
}
