# Harness 用户存储

`gcs-webnora` 的文件与 Harness 使用同一 JFS，按知识库所属空间的固定平台用户 ID 保存：

```text
/storage-root-jfs/
  user-<userID>/
    dsh-home/                  # 保持 Harness 原配置和历史
    workspace/                 # 保持默认工作区
    gcs-webnora/spaces/<空间ID>/files/
      <文档ID>/<文件>           # 原始文件、附件及落盘资源
      exports/<文件>           # 导出和其他字节资源
  harness-system/gcs-webnora/
    redis/                     # 服务共用 AOF/RDB，不挂入 Harness
    backups/                   # 迁移前数据库快照、Redis 副本及文件校验清单
```

逻辑 `resource://`、`storage://` 和 `local://<空间ID>/...` 引用不变。数据库仍使用既有 PostgreSQL 卷，包含原有向量和全文索引。解析临时文件、构建缓存留在本机，可重建的数据不迁入 JFS。

## 所属与共享

`platform_storage_bindings` 固定空间到认证服务和平台 userID 的映射。首次写入依据数据库中的有效 Owner 成员和 SSO 身份建立，不能使用请求参数或当前查看者的 userID；缺失、含糊或非法的身份立即失败，不回落到旧目录。

共享空间仅引用源知识库。成员读取源文件之前仍由现有后端检查权限，退出共享空间后不能凭旧知识库 ID、文件引用或旧选择继续读取。共享不复制文件、不把他人目录挂入用户 Harness。空间 Owner 转让不自动移动文件；存储迁移是另一个有备份的管理操作。

后端使用独立 UID/GID（230 默认 `21090:21090`）、drop ALL、no-new-privileges。每个 `gcs-webnora` 子目录归服务账号，模式 0700，文件 0600；不改变已有 `dsh-home` 和 `workspace` 的权限。以 `os.Root` 限制物理操作，拒绝命名空间符号链接和越界路径。用户容器仍只挂本人的用户根，不能得到知识库服务的 JFS 根挂载。

用户根目录由现有 Worker 在 launch 时创建，或由存储管理员预置；知识库服务只自动创建自己的子目录，不把用户根改成服务账号所有。未预置的用户根报错，不能回落到本机目录。独立于 Harness 直接接入知识库的新用户也必须先由存储侧预置用户根。

## 配置

```dotenv
LOCAL_STORAGE_LAYOUT=platform-user
LOCAL_STORAGE_BASE_DIR=/storage-root-jfs
LOCAL_STORAGE_LEGACY_BASE_DIR=/runtime/data/files
WEBNORA_USER_STORAGE_ROOT=/storage-root-jfs
WEBNORA_SERVICE_STORAGE_ROOT=/storage-root-jfs/harness-system/gcs-webnora
WEBNORA_APP_UID=21090
WEBNORA_APP_GID=21090
```

前两个 `LOCAL_STORAGE_*` 值选择新布局；`LEGACY_BASE_DIR` 仅将旧绝对引用转换为新布局内的对象位置，不读取旧数据作为兜底。未启用此布局的独立 WeKnora 部署继续使用原有平面 local 存储。不同存储实例的 local path prefix 只在当前空间 files 子目录内生效。

230 的 `deploy/dev-230.compose.yml` 已配置此布局，只给可信 app 挂 JFS，Redis 只挂自己的服务目录。第一次升级必须先迁移，不能直接启动新版，否则会找错旧文件。

## 迁移现有数据

1. 在隔离目录完成 Go 测试和新版本构建，先验证 JFS 的写入、权限与独立 Redis AOF 重启恢复。确认 JFS 元数据服务不依赖待迁移的 Redis，避免循环依赖。
2. 执行 `python3 scripts/migrate-user-storage.py` 查看只读计划；无法唯一映射的空间、孤立文件、符号链接必须先处理，不能猜测归属。
3. 暂停自动重载/同步，停止 app 等写入者，等待后台任务停止，再停止 Redis。保留旧启动配置和可执行文件，按迁移机制应用 PostgreSQL 138（SQLite 54）建表。
4. 执行 `python3 scripts/migrate-user-storage.py --apply`。脚本先备份 PostgreSQL 和 Redis，复制文件并逐个检查 SHA256；既有不同内容的目标拒绝覆盖。源目录保留，不改数据库里的资源引用。
5. 只为服务账号准备 `/runtime/bin`、`tmp`、`logs`、`home`、Go 缓存等必需写目录。不要把整个 JFS 或数据库目录改属主，不放宽用户私有目录权限。
6. 用 Compose 启动 Redis 和 app，检查其实际挂载、账号、健康状态及迁移版本。没有挂载的目录必须报错，禁止自动创建本地同名根作兜底。
7. 核对全部既有文件校验，验证原文下载、上传解析、图片、复制、导出、删除、检索、共享与撤权，并重启检查 Redis 持久化。验证前不清理旧源数据。

启动示例：

```bash
docker compose --env-file .env -p gcs-webnora-dev \
  -f deploy/dev-230.compose.yml \
  up -d --no-build redis app
```

回退必须先停写，并判断新版是否已产生新文件/任务。可以回退进程，但不能用旧快照覆盖已产生的新历史。迁移清单是恢复依据，旧源副本是备份，均不是自动清理目标。
