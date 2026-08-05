# EmuNet Link Console

静态浏览器链路控制台，可由 LinkServer 直接托管。

## 使用的后端接口

- `GET /api/v1/health`
- `GET /api/v1/emunets`
- `POST /api/v1/emunets`
- `POST /api/v1/emunets/{namespace}/{name}/stop`
- `GET /api/v1/emunets/{namespace}/{name}/delete-status`
- `GET /api/v1/emunets/{namespace}/{name}/summary`
- `GET /api/v1/emunets/{namespace}/{name}/pods?offset=0&limit=100`
- `POST /api/v1/ebpf/entry/by-pods`
- `DELETE /api/v1/ebpf/entry/by-pods`
- `POST /api/v1/ping/by-pods`

拓扑规则面板在浏览器中分页读取 Pod 详情，按选定规则生成节点位置和链路参数，再通过 `POST /api/v1/ebpf/entry/by-pods` 分批下发。

重新构建并部署 LinkServer 后，打开 `http://<master-ip>:30082/` 即可使用。页面会自动把 LinkServer 地址设置为当前访问地址。

如果直接用 `file://` 打开页面，浏览器可能因为跨域策略拦截请求；生产使用推荐从 LinkServer 根路径访问。
