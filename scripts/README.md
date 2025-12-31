## 生产环境构建与启动脚本

目标:

 - 使用 `-ldflags -X ...` 注入 `Version`/`Commit`/`BuildDate`, 避免启动时显示 `dev/none/unknown`.
 - 设置 `GIN_MODE=release` 与 `CGO_ENABLED=0`.
 - 约束默认情况下不允许 dirty 工作区直接构建(避免版本号出现 `-dirty`).
 - 约束生产版本号必须来自 git tag, 只输出 `v6.6.74` 这种格式, 不输出 `-gxxxx`.

### Windows (PowerShell)

```powershell
.\scripts\start-prod.ps1 -ConfigPath .\config.yaml
```

允许 dirty 工作区构建:

```powershell
.\scripts\start-prod.ps1 -ConfigPath .\config.yaml -AllowDirty
```

### Linux / macOS (sh)

```sh
chmod +x ./scripts/start-prod.sh
./scripts/start-prod.sh
```

指定配置:

```sh
CONFIG_PATH=./config.yaml ./scripts/start-prod.sh
```

允许 dirty 工作区构建:

```sh
ALLOW_DIRTY=1 ./scripts/start-prod.sh
```

### 版本号从哪里看

 - 运行时版本: 启动日志第一行 `CLIProxyAPI Version: ...`.
 - 管理接口响应头: `X-CPA-VERSION`, `X-CPA-COMMIT`, `X-CPA-BUILD-DATE`.
 - `GET /v0/management/latest-version` 返回的是 GitHub 最新 release 版本, 用于升级检查, 不是当前运行二进制的版本.

说明:

 - 启动脚本默认要求 clean 工作区, 避免不可复现的构建.
 - 生产构建脚本使用 `git describe --tags --abbrev=0` 取最近的 tag, 只输出 `v6.6.74` 这种格式.

### Docker 生产构建与启动

注意:

 - Dockerfile 现在强制要求注入 `VERSION/COMMIT/BUILD_DATE`, 否则构建会失败(避免出现 dev/none/unknown).
 - 生产环境建议在 clean 工作区构建, 默认脚本会拒绝 dirty.

Windows:

```powershell
.\scripts\docker-prod.ps1
```

macOS/Linux:

```sh
chmod +x ./scripts/docker-prod.sh
./scripts/docker-prod.sh
```
