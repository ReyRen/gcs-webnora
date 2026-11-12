# 工具链平台集成 WeKnora

主平台在 `https://172.18.127.67` 登录注册后，通过导航中的 iframe 打开 WeKnora。
本项目实现专用 SSO 适配和 iframe 握手；`model/master` 仅用于核对现有封装，没有修改该仓库。

知识库工作台构建的模型管理统一由 Harness 原生“设置 → 模型设置”承接，不再提供模型、Ollama 或模型目录的重复管理页。知识库、智能体及任务仍按原有逻辑从同一空间可用模型中选择对话、嵌入等模型；不删除模型记录，也不改变已有索引绑定。模型选择器的管理操作向配置允许的 Harness 父窗口发送 `weknora:open-model-settings`，Harness 的 `weknora:models-updated` 通知经过窗口和来源校验后刷新模型缓存。`VITE_KNOWLEDGE_WORKSPACE=false` 的独立构建保留原管理入口。

## 认证流程

1. 主平台加载 `/weknora/sso/platform`。入口会清理之前账号的本地登录态。
2. iframe 向直接父窗口发送 `weknora:sso-ready`，携带本次随机 nonce 和 client_id；当前联调临时使用 `targetOrigin: '*'`。
3. 父页面调用现有 `getSsoToken(clientId)`，利用主平台自身的 Cookie 取得票据。
4. 父页面校验 iframe 的 origin/source，用明确的 targetOrigin 返回 `weknora:sso-token`。
5. iframe 校验 parent、origin、nonce，向自身后端 POST `/api/v1/auth/platform-sso/exchange`。
6. 后端 POST 上游 `/UserLoginDisplayInfo` 验票，严格检查业务码 `0000`、JWT 的 client_id、userId 和过期时间；未验票前不信任 JWT 内容。
7. 后端按“固定认证服务地址 + userId”绑定本地用户，签发原有格式的本地登录凭据，进入知识库。

上游 Token 不放跳转 URL、日志或前端持久化存储。服务端保存的上游 access/refresh token 由 `SYSTEM_AES_KEY` 加密。
Client Secret 仅在后端用于刷新，不能放入 model 或 iframe 消息。

消息类型必须使用完整名称：子页发送 `weknora:sso-ready`，父页回传 `weknora:sso-token`；没有省略前缀的别名。
子页 Console 会输出不含 Token 内容的诊断信息：`[SSO] entry-mounted`、`config-ready`、`ready-posted`。
`ready-posted` 表示已调用 postMessage，父页是否收到仍需在父页监听器确认。
收到回传消息时，`token-message-rejected` 会列出来源、父窗口和 nonce 是否匹配；`unexpected-message-type` 提示消息类型不符。
这些浏览器消息本身不出现在服务器日志中，后端收到 `/auth/platform-sso/exchange` 请求才说明进入服务端换证阶段。

## 服务端配置

本次联调使用独立客户端 `gcs-webnora`，须与主平台登记一致。正式密钥只配置在被 Git 忽略的 `.env` 中，不写入前端或本文：

```dotenv
PLATFORM_SSO_ENABLED=true
PLATFORM_SSO_ONLY_LOGIN=true
PLATFORM_SSO_BASE_URL=http://172.18.127.67:8018/DistributedAISystem
PLATFORM_SSO_CLIENT_ID=gcs-webnora
PLATFORM_SSO_CLIENT_SECRET=<该客户端的正式密钥>
PLATFORM_SSO_PARENT_ORIGINS=dsh-app://app,https://172.18.127.67,http://localhost:3002,https://172.18.127.67:3002
SYSTEM_AES_KEY=<保留现有的32字节密钥>
SSRF_WHITELIST_EXTRA=172.18.127.67
APP_EXTERNAL_URL=https://172.18.127.67/weknora
FRONTEND_BASE_URL=https://172.18.127.67/weknora
```

合并现有 `SSRF_WHITELIST_EXTRA`，不要覆盖其他已放行主机。关闭 SSO 或缺少配置时入口不会授予匿名权限。
启动自动应用 PostgreSQL 137 / SQLite 53 迁移，增加身份映射和会话记录，原业务接口及用户主表结构保持兼容。
旧版部署已占用 PostgreSQL 136 / SQLite 52 的 SSO 数据会保留，并补齐上游同编号的凭据库表；SQLite 的重复列通过事务兼容升级处理。

当前部署启用 `PLATFORM_SSO_ONLY_LOGIN=true`：`/login` 和 `/register` 返回普通 404，不提供独立登录或跳转提示页。

知识管理界面的账户、成员及邀请人优先显示 SSO 已保存的平台邮箱；内部用户名和 UID 绑定不变。没有真实邮箱时才回退到用户名，不展示 `@platform-sso.invalid` 占位地址。

知识库和智能体的共享者、创建者展示标签在读取时按当前账号信息解析，也优先使用真实邮箱；`shared_by_username` 是现有响应中的展示标签字段，不是登录或授权依据。历史分享记录只存用户 ID，不需要重建分享或批量修改内部用户名。操作记录有可解析账号信息时使用同一邮箱展示规则；已删除账号或未保存邮箱的旧审计快照保留原有名称/ID 回退，不重写审计原文。外部数据源、凭据库和登录表单中的用户名是实际凭据，不作展示替换。

SSO 新建的个人空间直接以已验证的平台邮箱命名；平台没有邮箱时使用显示名，不带内部用户名的随机后缀。已有 PostgreSQL 部署可在备份空间名称后，用 `psql -v ON_ERROR_STOP=1 -f scripts/normalize-platform-workspace-names.sql` 修正历史默认名称。脚本只处理 SSO 身份、邮箱、默认空间和有效 Owner 成员关系完全对应，且名称仍为系统生成格式的记录；自定义空间名称保持不变。脚本可重复执行，不修改空间 ID、内部用户名或成员权限。前端重新获取成员关系时同步更新记住的空间名称，无需清理浏览器存储。

被邀请人接受站内邀请后，立即刷新成员身份及打开着的知识库、共享资源和空间列表，并可直接点击“进入空间”。身份刷新失败会保留重试入口，不重复接受邀请；切换空间沿用现有的租户隔离与导航流程。
iframe 打开 `/weknora/` 或 `/weknora/sso/platform` 后先加载 JS、显示 loading，再发送 ready 并等待父页回传票据。
SSO 入口不在握手前恢复缓存的账号凭据，避免过期的旧令牌触发 `/login` 导航；验证新票据后再获取账号及空间权限。普通业务页面仍恢复已保存的会话。
网关拒绝明确的顶层文档请求；缺少 `Sec-Fetch-Dest` 时允许加载 SPA，由浏览器窗口判断让独立访问显示 404。
HTTP IP 地址可能不带 Fetch Metadata，不能以缺少该头作为拒绝 iframe 的条件。身份仍以父页面来源、nonce 和后端验票为准。
5174 开发服务仅监听 230 本机回环地址，不再对外提供独立访问入口。
本地密码登录、公开/邀请注册、Lite 自动登录及其他 OIDC 登录入口返回 403，旧的非 SSO 用户凭据也不能继续访问或刷新。
SSO 验票成功后的内部自动开户、用户绑定、空间和权限仍沿用原有逻辑；API Key 服务接口继续按原权限规则工作。
用户设置中隐藏本地密码修改入口。只退出 WeKnora 时显示简短的已退出状态，重新打开菜单后再登录。
如需恢复独立登录能力，由管理员修改此开关、开发服务监听地址和网关页面访问限制，再重新启动相关服务。

已有同邮箱的本地账号默认不自动合并。经管理员确认后，可配置准确的邮箱列表：

```dotenv
PLATFORM_SSO_LINK_EXISTING_EMAILS=renyuan@test.com
```

只有上游实际返回相同 userAccount 的用户可首次绑定该账号。后续仍按 userId 绑定。
上游账号没有邮箱时，新用户使用 `.invalid` 域内部占位地址满足现有数据约束，不以其识别外部身份。
自动开户遵循现有默认空间策略，不把上游 roleId/companyId 自动解释为系统管理员或跨空间权限。

## iframe 与 HTTPS

HTTPS 主页面不能直接嵌入 `http://172.18.36.230:5174/`。
建议通过主平台现有 HTTPS 网关代理 `/weknora/`；与主平台同源也避免第三方存储限制。
本项目提供独立的子路径构建和 HTTP 上游 `172.18.36.230:5175`；Vite 5174 仅供服务器本机开发使用。

在 230 项目根目录执行：

```bash
docker exec -e VITE_BASE_PATH=/weknora/ gcs-webnora-frontend-dev npm run build -- --outDir /runtime/platform-dist
docker compose --env-file .env -p gcs-webnora-dev -f deploy/dev-230.compose.yml --profile platform-sso up -d --force-recreate platform-frontend
```

网关配置通过单文件挂载；修改后需重新创建该容器，让 Mutagen 同步后的新文件挂载生效。

主平台维护者把以下 location 加入现有 HTTPS server 并按正常发布流程 reload，不需要修改主平台后端认证接口：

```nginx
location = /weknora { return 308 /weknora/; }
location ^~ /weknora/ {
    proxy_pass http://172.18.36.230:5175/weknora/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Sec-Fetch-Dest $http_sec_fetch_dest;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_buffering off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
}
```

`$connection_upgrade` 需要沿用主平台 http 区域现有 map。发布前执行 `nginx -t`。
保留浏览器的 `Sec-Fetch-Dest`；没有该头时不在网关误拒绝 iframe。SSO 验票和消息来源校验仍按认证流程执行。
当前联调临时使用 `frame-ancestors * dsh-app://app`，放开 HTTP(S) 祖先并显式保留 DSH 自定义协议；该策略检查整条祖先链。CSP 的 `*` 不自动包含任意非 HTTP(S) 协议，参见 [CSP 匹配规则](https://www.w3.org/TR/CSP3/#match-url-to-source-expression)。
SSO 的 `PLATFORM_SSO_PARENT_ORIGINS` 校验直接发送 Token 的父窗口，当前允许 `dsh-app://app` 桌面父窗口、`https://172.18.127.67` 主平台，以及联调父页面 `http://localhost:3002` 和 `https://172.18.127.67:3002`。各地址只精确允许列出的协议和端口，本地地址在联调结束后移除。
`weknora:sso-ready` 临时向直接父窗口使用 `targetOrigin: '*'`，不携带 Token 或用户信息；认证成功和错误通知仍只发送到配置允许的精确来源。
接收 Token 和退出通知时仍检查直接父窗口及精确 Origin；登录 Token 还必须匹配本次 nonce，并由后端验证。`PLATFORM_SSO_PARENT_ORIGINS` 不接受其他自定义协议 host、路径或通配符。
排查结束后，将 `platformSso.ts` 的 ready 目标恢复为 `platformSsoMessageTargets(origins)`，并将网关的祖先列表恢复为 `'self' https://172.18.127.67 dsh-app://app`。

## model/master 维护者需要的改动

1. 新增一个使用 iframe 的菜单路由，示例组件在 [examples/WeKnoraFrame.vue](examples/WeKnoraFrame.vue)。使用主平台现有 `getSsoToken`，不读取或复用用户密码。
2. 按主平台登记清单同步 api-paths、service-registry、runtime-services、开发代理及 HTTPS Nginx location。前端路径与本项目 `/weknora/` 构建一致。
3. 退出统一平台时，调用文档接口 `/sso/logout`，并在移除 iframe 前调用组件暴露的 logout，以主动撤销本地登录态。
4. 主平台切换用户时销毁旧 iframe、重新加载入口，不能复用旧账号已登录的 iframe。

这里是交接清单和参考组件，没有修改 model 仓库文件。

## 续期与联动退出

SSO 会话每 30 秒重新验票一次。上游明确返回无效时，本地 access/refresh token 均拒绝继续使用。
上游短暂超时或 5xx 返回 503，不会当作用户注销，也不会清空前端凭据。
接近过期时，通过 multipart form-data 调用 `/oauth/token`。数据库行锁串行处理跨实例刷新，并原子保存旋转后的 token 对。
没有配置 Client Secret 或没有上游 refresh_token 时，过期后 iframe 自动回到 SSO 入口，由父平台重新领票。

主平台必须实际撤销上游 token：只清除 JSESSIONID、隐藏 iframe 或只解码 JWT 都不能保证全局退出。
需要与真实平台确认 `/sso/logout` 后 UserLoginDisplayInfo 是否立即拒绝旧 access_token。
上述 30 秒是服务端有界复核窗口；若要求实时关闭已经打开的页面/长连接，需要平台主动注销通知。

## 验收

- 同一平台账号重复进入仍对应一个本地账号。
- 更换账号后不会读取前一账号的空间和凭据。
- 错误客户端、过期票据、业务失败码、伪造来源/nonce 均被拒绝。
- 上游注销后，本地凭据不能独立续期或通过切换空间脱离 SSO 关联。
- 原本地登录、空间 RBAC、聊天 SSE、文件和 WebSocket 路径继续工作。

本项目提供自动化模拟上游测试。生产 SSO 客户端配置、主平台网关发布及真实账号的浏览器联调仍需主平台维护者配合。
