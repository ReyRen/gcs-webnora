# 230 开发环境

本地 `E:\Projects\gcs-webnora` 修改源码，Mutagen 同步到
`172.18.36.230:/storage-md0/renyuan/gcs-webnora`，在服务器运行服务。

- 开发前端：`http://127.0.0.1:5174/`，仅服务器本机可访问，Vite 热更新。
- 主平台集成使用 5175 子路径构建及 HTTPS 代理，详见 [SSO 接入说明](../docs/integrations/toolchain-platform-sso.md)。
- 后端：<http://172.18.36.230:18090/health>，Air 自动编译并重启 Go 服务。
- PostgreSQL、Redis、Docreader 仅监听服务器回环地址。
- 数据、依赖、工具链和缓存保存在 `/storage-md0/renyuan/.gcs-webnora-runtime`，位于同步目录之外。
- 运行配置和生成的密钥保存在根目录 `.env`，不提交 Git。

在服务器项目根目录执行：

```bash
cd /storage-md0/renyuan/gcs-webnora
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml up -d
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml ps
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml logs -f --tail=100 app frontend
```

Go 和 Vite 源码修改同步后自动生效；主平台集成的静态前端需按接入说明重新构建。
修改 `.env` 或 Compose 配置后执行 `up -d` 重新创建受影响的服务。
内网模型主机通过 `.env` 中的 `SSRF_WHITELIST_EXTRA` 放行，当前为 `172.18.127.67,172.18.36.230`；新增主机时保留已有条目。修改此项后仅重新创建 `app`，使新环境变量进入后端进程。
修改前端依赖或锁文件后执行：

```bash
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml exec frontend npm ci --no-audit --no-fund
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml restart frontend
```

Python Docreader 使用镜像提供运行依赖，并挂载当前项目源码；修改其源码后执行 `restart docreader`。
修改 Python 依赖需要重新准备对应镜像。

首次部署已在服务器独立安装 Go 1.26.0，并复用现有 Go 构建基础镜像和 Node 24 镜像。
此 Compose 文件依赖 `.env` 中的 `WEBNORA_RUNTIME_DIR` 和 `WEBNORA_NODE_IMAGE`；不是其他服务器的通用安装器。

停止此项目而保留数据：

```bash
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml down
```

用户通过主平台注册和登录，SSO 验票后自动开通 WeKnora 账号。模型密钥由使用者在页面填写。

## Harness 知识库界面

此分支默认使用知识库工作区布局：移除常驻侧栏、头像和账号菜单，顶部保留知识库、共享空间、空间切换、通知与配置。
知识库创建、文档上传与解析、检索设置、共享和成员管理继续使用原有组件及权限。
模型由 Harness 原生模型设置统一管理，各知识库从当前用户的可用目录选用。解析器、向量库、存储、API / MCP 服务和系统配置按原有角色权限保留。
内置对话、智能体、产物、自动化、工具箱和助手入口不再显示；旧业务路由返回知识库列表。
后端接口保持可用，Harness 插件仍可调用检索和问答接口；统一登录流程保持不变。

iframe 继续使用 `http://172.18.36.230:5175/weknora/sso/platform`，无需改 Harness 接入地址。
在 230 构建静态页面：

```bash
docker exec -e VITE_BASE_PATH=/weknora/ gcs-webnora-frontend-dev npm run build -- --outDir /runtime/platform-dist
```

如需构建原完整界面，可在构建时增加 `-e VITE_KNOWLEDGE_WORKSPACE=false`。
此开关只控制前端布局和入口，不替代后端权限校验。

## 上游同步约定

2026-11-10 合并腾讯上游 `cbdcebabf48c5ee2d6868b370326aa8341703f62`，纳入基线后的 82 个提交。
保留统一平台专用验票、用户与空间绑定、iframe 握手及知识库工作区布局；检索、解析、分块和其他业务更新沿用上游。
上游完整覆盖同一需求时采用上游实现；同一文件发生变化不代表功能重复，不能据此整文件覆盖。

每次更新先提交现有二开，再拉取上游 `main` 并逐项核对功能。上游实现已满足相同需求时优先采用上游；
仍需保留的平台登录、身份与空间隔离、iframe 接入和知识库界面要求继续保留，并验证接口兼容性。

Mutagen 源码同步、230 服务部署与 GitHub 发布分别核对。完成发布后，确认 fork 的 `main` 提交号与本地一致；
不能把服务器已生效视为 GitHub 已提交，也不强制覆盖远端历史。

上游凭据库保留 PostgreSQL 136 / SQLite 52；本分支 SSO 改为 137 / 53。
升级会补齐旧 SSO 部署缺失的凭据库表，并保留身份映射、加密会话和本地 Token 关联；SQLite 旧版本使用事务桥接。
迁移回归覆盖全新库、上游库、旧 SSO 库，以及重复启动。

前端完整测试包含 Bash 交互测试；Alpine Node 镜像需先安装 `bash`，运行 Vite 本身不依赖它。
## 用户文件存储

Harness 集成部署的用户文件、Redis 与受限账号配置见[用户存储与迁移](../docs/integrations/user-storage.md)。当前 Compose 已使用用户 JFS 目录；现有本地文件部署第一次升级时必须先停写、备份并执行迁移。
