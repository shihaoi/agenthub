package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"agenthub/internal/auth"
	"agenthub/internal/db"
	"agenthub/internal/gitrepo"
)

// Config 包含了在建立服务端进程之前所需要决定的全部宏观指标参数和设定的属性组合。
type Config struct {
	MaxBundleSize    int64  // 用于防爆库与内存溢出而限制的一次接收包裹二进制数据的最大重量级(以字节算)
	MaxPushesPerHour int    // 定义了每一位挂载进来的 agent 它在单小时最多能抛多少次 git 变更上去合并
	MaxPostsPerHour  int    // 控制这些在活跃池里的参与对象发布聊天言论的最高合法配额
	ListenAddr       string // 指示系统要在当前机器开启那个网络服务和指定的暴露数字（比方说是 ":8080"）
}

// Server 指代的就是在正常应用环境之下，封装聚合了全链路必需模块资源（如持久化连接，git操作底层，网络分配通道，关键配给配置）的主程序对象实例。
type Server struct {
	db       *db.DB         // 存有多路复用长连与数据对象映射的实体存储操控模块
	repo     *gitrepo.Repo  // 负责与外部隔离并实现对硬盘实质性代码仓库改变产生动作反应的核心组件
	adminKey string         // 用于识别特定具有全部系统管理干涉职权的持有者关键明文认证的字符串通行证
	mux      *http.ServeMux // Go 原生提供的专门处理各类不同模式字符地址派发分配执行对应方法的路由引流中心
	config   Config         // 这个服务应用在运行时遵从的上限阈值规则定义集
}

// New 生成出这台 Server 设备的完全装配体而且同时帮助设置绑定完了全系统里面所有应该具有的对接路线。
func New(database *db.DB, repo *gitrepo.Repo, adminKey string, cfg Config) *Server {
	s := &Server{
		db:       database,
		repo:     repo,
		adminKey: adminKey,
		// 使用标准内建生成器以赋予完全初始态的干干净净可拓展多用路由接轨部件。
		mux:      http.NewServeMux(),
		config:   cfg,
	}
	s.setupRoutes() // 在构造时主动装上全部齿轮
	return s
}

// setupRoutes 将每一个我们规定好拥有特种功能的行为与对应的统一接入网络资源符（URI）加以固定连接绑定。
func (s *Server) setupRoutes() {
	// 初始化两款用于确保数据进入应用内部安全的第一层闸口守卫 (Auth 中间件)
	authMw := auth.Middleware(s.db)        // 要求持有普通的被认可入库 agent 令牌者放行。
	adminMw := auth.AdminMiddleware(s.adminKey) // 唯有拥有开通时最高密钥的管理员才能通过的严苛关卡。

	// Git endpoints （所有发生实际代码合并与交流相关探究的操作汇聚）
	// 注意这里路由路径包裹着 authMw，即必须过检。
	s.mux.Handle("POST /api/git/push", authMw(http.HandlerFunc(s.handleGitPush)))
	s.mux.Handle("GET /api/git/fetch/{hash}", authMw(http.HandlerFunc(s.handleGitFetch)))
	s.mux.Handle("GET /api/git/commits", authMw(http.HandlerFunc(s.handleListCommits)))
	s.mux.Handle("GET /api/git/commits/{hash}", authMw(http.HandlerFunc(s.handleGetCommit)))
	s.mux.Handle("GET /api/git/commits/{hash}/children", authMw(http.HandlerFunc(s.handleGetChildren)))
	s.mux.Handle("GET /api/git/commits/{hash}/lineage", authMw(http.HandlerFunc(s.handleGetLineage)))
	s.mux.Handle("GET /api/git/leaves", authMw(http.HandlerFunc(s.handleGetLeaves)))
	s.mux.Handle("GET /api/git/diff/{hash_a}/{hash_b}", authMw(http.HandlerFunc(s.handleDiff)))

	// Message board endpoints （用来给各端发话或通传社交指令的集中交流板版块）
	s.mux.Handle("GET /api/channels", authMw(http.HandlerFunc(s.handleListChannels)))
	s.mux.Handle("POST /api/channels", authMw(http.HandlerFunc(s.handleCreateChannel)))
	s.mux.Handle("GET /api/channels/{name}/posts", authMw(http.HandlerFunc(s.handleListPosts)))
	s.mux.Handle("POST /api/channels/{name}/posts", authMw(http.HandlerFunc(s.handleCreatePost)))
	s.mux.Handle("GET /api/posts/{id}", authMw(http.HandlerFunc(s.handleGetPost)))
	s.mux.Handle("GET /api/posts/{id}/replies", authMw(http.HandlerFunc(s.handleGetReplies)))

	// Admin endpoints (超高权限敏感操作区如生成发卡等能力只此一家)
	s.mux.Handle("POST /api/admin/agents", adminMw(http.HandlerFunc(s.handleCreateAgent)))

	// Public registration (对外暴露任何人甚至机器无门槛自动申请开卡的地点，只受到系统内部的连击阻挡拦截)
	s.mux.HandleFunc("POST /api/register", s.handleRegister)

	// Health check (不用设防用于运维探测是否程序进程当掉或者是还在运行的报平安接口)
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Dashboard (公共看板大屏页读取显示口，它不含有控制力，所以为了让更多人知道概况不用强加通行证)
	s.mux.HandleFunc("GET /", s.handleDashboard)
}

// ListenAndServe 将本包装后的综合服务对象插上电，激活底层的 http.ListenAndServe 让它开始堵塞并且源源不断的循环接送处理流量。
func (s *Server) ListenAndServe() error {
	log.Printf("listening on %s", s.config.ListenAddr)
	return http.ListenAndServe(s.config.ListenAddr, s.mux)
}

// --- JSON helpers （工具系列：便捷的复用功能去实现反复出现的打包或发送）---

// writeJSON 将一切输入的目标结构模型编码成为一串统一规范的标准 JS 对象形式文字发送给访问人。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json") // 加盖标准章指示数据类型
	w.WriteHeader(status)                              // 按指定发特定的 HTTP 代码提示顺利或是别的
	json.NewEncoder(w).Encode(v)                       // 实现压模出货
}

// writeError 用固定的含有错误通告格式的消息给那些调用不规整导致报错的人一种可以代码判断的 JSON 输出答复。
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON 实现对收到底的带身文字通过限制手段来解码成内存对象的过程。
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	// 在 JSON 端点上控制请求的大小并一刀切掉多出 64KB 以上的所有后续内容以极大地免于出现坏人们借此淹没堆栈的意图。
	limited := io.LimitReader(r.Body, 64*1024)
	return json.NewDecoder(limited).Decode(v)
}
