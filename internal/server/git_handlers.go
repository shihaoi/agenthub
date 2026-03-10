package server

import (
	"io"
	"net/http"
	"os"
	"strconv"

	"agenthub/internal/auth"
	"agenthub/internal/db"
	"agenthub/internal/gitrepo"
)

// handleGitPush 处理向服务器传送并集成用户本地离线 git 打包 bundle 资源的端点（即：将代码合进远程的这个裸仓库里面）。
func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) {
	// 任何对服务端的改变动作必须通过挂在在 context (之前已被安全中间件检查注入) 上面的验证数据来进行拦截以知道操作元凶身份。
	agent := auth.AgentFromContext(r.Context())

	// 这个操作会进行密集的硬盘 I/O 解压缩而且会操作锁等因此有必要使用限流，默认大概一小时不超过 100 次动作：
	allowed, err := s.db.CheckRateLimit(agent.ID, "push", s.config.MaxPushesPerHour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rate limit check failed")
		return
	}
	// 超发则制止：
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "push rate limit exceeded")
		return
	}

	// 为保护服务端不被庞然大物超大的二进制流量冲爆而设定网络载荷天花板限制（通过使用 MaxBytesReader 做防护截断）
	r.Body = http.MaxBytesReader(w, r.Body, s.config.MaxBundleSize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "bundle too large")
		return
	}

	// 因为系统外挂命令需要的是实在落地的磁盘文件而无法吃流所以在操作之前必须要弄出一个拥有绝对路径地址的残余包文件
	tmpFile, err := os.CreateTemp("", "arhub-push-*.bundle")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create temp file")
		return
	}
	defer os.Remove(tmpFile.Name()) // 在退出此 API 逻辑处理区块之时负责消抹这些痕迹

	// 把接在网络缓冲上截取好的字节全都塞进这个生成的系统临时占位文档中。
	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		writeError(w, http.StatusInternalServerError, "failed to write bundle")
		return
	}
	// 写完了务必马上关上管道锁以便后面的控制台命令行能够正当地打开它。
	tmpFile.Close()

	// 把新收到的代码集成引入裸版(bare)的核心母版本库里面。在处理同时将会吐出这批包里新记录的顶点树哈希序列
	hashes, err := s.repo.Unbundle(tmpFile.Name())
	if err != nil {
		// 返回解析或者打包冲突/缺陷所导致不可通过之类报错。
		writeError(w, http.StatusBadRequest, "invalid bundle: "+err.Error())
		return
	}

	// 解析完了需要进一层次往 SQL 索引中收录好以支持将来的轻便调用：
	var indexed []string
	for _, hash := range hashes {
		// 先确认一下当前的树图到底登记在册了没有，有的话可以视为重发便置之并执行下一节点的核实。
		existing, _ := s.db.GetCommit(hash)
		if existing != nil {
			indexed = append(indexed, hash)
			continue
		}

		parentHash, message, err := s.repo.GetCommitInfo(hash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read commit info")
			return
		}

		// 除开这只是初始化（孤岛无父级）如果它是派生的但我们此时底层存储图里竟然没听说过他爸（其先决提条件没有达成）这是绝对不会被合法兼容并接受纳入库的行为：
		if parentHash != "" && !s.repo.CommitExists(parentHash) {
			writeError(w, http.StatusBadRequest, "parent commit not found: "+parentHash)
			return
		}

		// 若遇到父节点虽然真实存在于实际上的 bare repo 但是他竟然没被收纳记录进 SQL（可能是老历史或者是系统之前发生了异常缺位）那需要帮着做一次“补票”将其父辈做前置补充登记处理
		if parentHash != "" {
			if pc, _ := s.db.GetCommit(parentHash); pc == nil {
				// 获取父的信息。这个地方缺失 Agent ID 信息没关系留白就行。
				pParent, pMsg, _ := s.repo.GetCommitInfo(parentHash)
				s.db.InsertCommit(parentHash, pParent, "", pMsg)
			}
		}

		// 把一切准备齐备的此次操作内容彻底扎根进 db。
		if err := s.db.InsertCommit(hash, parentHash, agent.ID, message); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to index commit")
			return
		}
		indexed = append(indexed, hash) // 汇总所有进场确认无误的成功结果
	}

	// 对这整个批处理动作结算并且叠加一发其调用计次账单。
	s.db.IncrementRateLimit(agent.ID, "push")

	writeJSON(w, http.StatusCreated, map[string]any{
		"hashes": indexed,
	})
}

// handleGitFetch 处理想从线上提取同步代码进本机分支的人物的索要打包命令：
func (s *Server) handleGitFetch(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash") // 被客户端告知希望将某一个指定的具体 commit 打出返回。
	if !gitrepo.IsValidHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	// 库里面有这个特定的哈希资源才能开始打出
	if !s.repo.CommitExists(hash) {
		writeError(w, http.StatusNotFound, "commit not found")
		return
	}

	// 为客户端创建出一个包含相关历程结构体系的物理包文件
	bundlePath, err := s.repo.CreateBundle(hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create bundle")
		return
	}
	defer os.Remove(bundlePath) // 这里特别要求使用结束后不能保留产生占据空间的数据必须要执行删灭清理（无论何故）

	// 作为纯流二进制形式推送给用户而不是传统的解析类消息：
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+hash+".bundle")
	http.ServeFile(w, r, bundlePath) // 把底层的文件体发送到底层对接口进行传递发送
}

// handleListCommits 列举查出过往所有在案登记发生过提交合并活动的操作简要一览清单。允许传递具体作者标记过滤或者是作分页截取行为
func (s *Server) handleListCommits(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	commits, err := s.db.ListCommits(agentID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if commits == nil {
		commits = []db.Commit{}
	}
	writeJSON(w, http.StatusOK, commits)
}

// handleGetCommit 查询出特定的拥有一个专属指纹标识(hash)对应的提交实体其细则和详单。
func (s *Server) handleGetCommit(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !gitrepo.IsValidHash(hash) { // 检测非法注入或者拼写错误（没超过范围和特定字符集类型)
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	commit, err := s.db.GetCommit(hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if commit == nil {
		writeError(w, http.StatusNotFound, "commit not found")
		return
	}
	writeJSON(w, http.StatusOK, commit)
}

// handleGetChildren 专门找出究竟还有谁是在目前指派定的节点的基础上延伸、继承并发掘创立派生代码的人。
func (s *Server) handleGetChildren(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !gitrepo.IsValidHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	children, err := s.db.GetChildren(hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if children == nil {
		children = []db.Commit{}
	}
	writeJSON(w, http.StatusOK, children)
}

// handleGetLineage 用来挖掘展现它一路走来的直溯发展历程（即族谱）一直走到最上古无源头根基时停止的过程集合。
func (s *Server) handleGetLineage(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !gitrepo.IsValidHash(hash) {
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	lineage, err := s.db.GetLineage(hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if lineage == nil {
		lineage = []db.Commit{}
	}
	writeJSON(w, http.StatusOK, lineage)
}

// handleGetLeaves 把处于这结构中末端的最靠前列叶子结点拉出来展示给大家。
func (s *Server) handleGetLeaves(w http.ResponseWriter, r *http.Request) {
	leaves, err := s.db.GetLeaves()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}
	if leaves == nil {
		leaves = []db.Commit{}
	}
	writeJSON(w, http.StatusOK, leaves)
}

// handleDiff 用于生成用于区别对比任意两个 commit 快照所衍生导致的细微变更。
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	agent := auth.AgentFromContext(r.Context())
	// 频次控制在比较这件本身极为损费资源的行为操作上是不能妥协松散对待的。
	allowed, _ := s.db.CheckRateLimit(agent.ID, "diff", 60)
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "diff rate limit exceeded")
		return
	}

	hashA := r.PathValue("hash_a")
	hashB := r.PathValue("hash_b")
	if !gitrepo.IsValidHash(hashA) || !gitrepo.IsValidHash(hashB) {
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	// 最终生成两版的差距文件串
	diff, err := s.repo.Diff(hashA, hashB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "diff failed")
		return
	}

	s.db.IncrementRateLimit(agent.ID, "diff")
	// 作为比较字符串明文返回客户端以展示
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(diff))
}
