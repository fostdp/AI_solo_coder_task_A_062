# 智慧煤矿瓦斯抽采管网智能调控系统

## 系统架构

```
┌─────────────────────────────────────────────────────────────────┐
│                        前端 (Canvas + WS)                        │
│  pipe_network.js (管网图)  │  borehole_detail.js (钻孔详情)     │
│  app.js (状态/WS/数据获取)  │  index.html                       │
└──────────────────────────┬──────────────────────────────────────┘
                           │ HTTP / WebSocket
┌──────────────────────────▼──────────────────────────────────────┐
│                     Go 后端服务 (端口 8080/6060)                  │
│  ┌────────────┐  ┌─────────────────┐  ┌───────────────────┐    │
│  │  Handler    │  │ DataCollector   │  │ NetworkOptimizer  │    │
│  │ (HTTP路由)  │→│ (DataCh写入DB)  │  │ (OptimizeCh+GA)   │    │
│  └────────────┘  └─────────────────┘  └─────────┬─────────┘    │
│  ┌────────────┐  ┌─────────────────┐            │              │
│  │  Hub (WS)  │  │ AlarmMonitor    │            │              │
│  │ (广播推送) │←│ (TriggerCh告警)  │            │              │
│  └────────────┘  └─────────────────┘            │              │
│                           ↑                      ↓              │
│                  ┌──────────────────────────────────┐           │
│                  │    CommandDispatcher (CommandCh)  │           │
│                  │    (MQTT ACK追踪+重试+超时)       │           │
│                  └────────────┬─────────────────────┘           │
│                               │ pprof :6060                     │
└───────────────────────────────┼──────────────────────────────────┘
                                │ MQTT
          ┌─────────────────────┼─────────────────────┐
          │                     │                       │
┌─────────▼──────┐   ┌──────────▼──────┐   ┌──────────▼──────┐
│ Mosquitto      │   │ PLC 模拟器      │   │ DTU 模拟器      │
│ (MQTT Broker)  │   │ (接收指令+反馈) │   │ (600孔数据上报) │
│ :1883          │   │遗嘱消息+保留会话│   │ :8080→batch API │
└────────────────┘   └─────────────────┘   └─────────────────┘
          │
┌─────────▼──────────────────────────────────────────────────────┐
│  PostgreSQL + PostGIS (端口 5432)                                │
│  pump_stations / boreholes / pipelines (GiST空间索引)           │
│  borehole_data / pump_station_data (B-tree复合索引)             │
│  alerts / optimization_results / plc_commands                   │
└─────────────────────────────────────────────────────────────────┘
```

## 模块通信

| 通道 | 发送方 | 接收方 | 缓冲 | 用途 |
|------|--------|--------|------|------|
| `DataCh` | Handler | DataCollector | 256 | 钻孔/泵站数据入库 |
| `OptimizeCh` | Handler | NetworkOptimizer | 8 | 触发遗传算法优化 |
| `CommandCh` | NetworkOptimizer | CommandDispatcher | 512 | MQTT指令下发 |
| `TriggerCh` | AlarmMonitor内部 | AlarmMonitor | 1 | 周期性告警评估 |

## 快速部署

### 前置要求

- Docker 20.10+
- Docker Compose v2+

### 一键启动

```bash
cd backend
docker compose up -d --build
```

等待所有服务就绪后访问 http://localhost:8080

### 服务端口

| 服务 | 端口 | 说明 |
|------|------|------|
| Go 后端 | 8080 | HTTP API + WebSocket + 前端静态文件 |
| pprof | 6060 | Go 性能监控 |
| PostgreSQL | 5432 | 数据库 |
| Mosquitto | 1883 | MQTT Broker |

### 查看日志

```bash
docker compose logs -f backend       # Go 服务日志
docker compose logs -f dtu-simulator  # 钻孔模拟器日志
docker compose logs -f plc-simulator  # PLC 模拟器日志
docker compose logs -f postgres       # 数据库日志
```

### 停止服务

```bash
docker compose down
docker compose down -v   # 同时删除数据卷
```

## 遗传算法配置

配置文件：`backend/config/optimizer.json`

```json
{
  "population_size": 100,
  "max_generations": 200,
  "elite_count": 5,
  "tournament_size": 3,
  "mutation_rate": 0.1,
  "crossover_rate": 0.5,
  "pump_min": 20.0,
  "pump_max": 60.0,
  "valve_min": 0.0,
  "valve_max": 100.0,
  "max_optimization_time_seconds": 5,
  "stagnation_limit": 30
}
```

环境变量 `OPTIMIZER_CONFIG` 可指定配置路径，文件不存在时使用硬编码默认值。

## 模拟器配置

### DTU 钻孔数据模拟器

模拟 4G DTU 终端，定时向 Go 服务上报钻孔数据。

| 环境变量 | 命令行参数 | 默认值 | 说明 |
|----------|-----------|--------|------|
| `API_BASE` | `--api` | `http://localhost:8080` | 后端 API 地址 |
| `BOREHOLES` | `--boreholes` | 600 | 钻孔数量 |
| `INTERVAL` | `--interval` | 120 | 上报间隔（秒） |
| `CONC_MIN` | `--conc-min` | 8.0 | 基准浓度下限 (%) |
| `CONC_MAX` | `--conc-max` | 65.0 | 基准浓度上限 (%) |
| `FLOW_MIN` | `--flow-min` | 0.3 | 基准流量下限 (m³/min) |
| `FLOW_MAX` | `--flow-max` | 5.0 | 基准流量上限 (m³/min) |
| `NOISE_CONC` | `--noise-conc` | 2.0 | 浓度高斯噪声标准差 |
| `NOISE_FLOW` | `--noise-flow` | 0.2 | 流量高斯噪声标准差 |
| - | `--no-batch` | false | 禁用批量API，逐条POST |

Docker Compose 示例：
```yaml
dtu-simulator:
  environment:
    BOREHOLES: 600
    INTERVAL: 120
    CONC_MIN: 5.0
    CONC_MAX: 70.0
```

本地运行：
```bash
pip install requests
python scripts/dtu_simulator.py --boreholes 600 --interval 120 --conc-min 5 --conc-max 70
```

### PLC 模拟器

模拟现场 PLC，接收 MQTT 调控指令并反馈执行结果。

| 环境变量 | 命令行参数 | 默认值 | 说明 |
|----------|-----------|--------|------|
| `MQTT_BROKER` | `--broker` | localhost | MQTT Broker 地址 |
| `MQTT_PORT` | `--port` | 1883 | MQTT Broker 端口 |
| `FAIL_RATE` | `--fail-rate` | 0.05 | 指令执行失败率 (0.0-1.0) |
| `DELAY_MIN` | `--delay-min` | 0.5 | 最小执行延迟 (秒) |
| `DELAY_MAX` | `--delay-max` | 2.0 | 最大执行延迟 (秒) |

MQTT Topic 规范：
- 指令下发：`gas/plc/{type}/{id}/command` (QoS 1)
- 执行反馈：`gas/plc/{type}/{id}/feedback` (QoS 1)
- 在线状态：`gas/plc/simulator/status` (Retained + Will)
- 遗嘱消息：PLC 断开时自动发布 `{"status":"offline"}`

Docker Compose 示例：
```yaml
plc-simulator:
  environment:
    MQTT_BROKER: mqtt
    FAIL_RATE: 0.05
    DELAY_MIN: 0.5
    DELAY_MAX: 2.0
```

本地运行：
```bash
pip install paho-mqtt
python scripts/plc_simulator.py --broker localhost --fail-rate 0.1
```

## 性能监控 (pprof)

Go 服务在 6060 端口开启 pprof：

```bash
# 查看 goroutine 堆栈
go tool pprof http://localhost:6060/debug/pprof/goroutine

# CPU 采样 30 秒
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# 内存分配
go tool pprof http://localhost:6060/debug/pprof/heap

# 在浏览器中查看
open http://localhost:6060/debug/pprof/
```

## PostgreSQL 调优

docker-compose 中 PostgreSQL 已配置以下参数：

| 参数 | 值 | 说明 |
|------|----|------|
| `shared_buffers` | 256MB | 共享缓冲区 |
| `effective_cache_size` | 768MB | 查询规划器缓存估计 |
| `work_mem` | 4MB | 排序/哈希操作内存 |
| `maintenance_work_mem` | 128MB | 维护操作内存 |
| `max_connections` | 100 | 最大连接数 |

空间索引（init.sql 中自动创建）：

```sql
CREATE INDEX idx_pump_stations_geom ON pump_stations USING GIST(geom);
CREATE INDEX idx_boreholes_geom ON boreholes USING GIST(geom);
CREATE INDEX idx_pipelines_geom ON pipelines USING GIST(geom);
```

## MQTT Broker 配置

Mosquitto 配置要点：

- **持久化会话**：`persistence true`，数据存储在 Docker volume
- **保留消息**：`retain_available true`，`max_retained_messages 2000`
- **遗嘱消息**：PLC 模拟器注册 `will_set` 通知离线状态
- **QoS 1**：指令和反馈均使用 QoS 1 保证至少一次投递

## 环境变量汇总

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DB_HOST` | localhost | PostgreSQL 主机 |
| `DB_PORT` | 5432 | PostgreSQL 端口 |
| `DB_USER` | postgres | 数据库用户 |
| `DB_PASSWORD` | postgres | 数据库密码 |
| `DB_NAME` | gas_drainage | 数据库名 |
| `DB_MAX_CONNS` | 50 | 连接池最大连接数 |
| `MQTT_BROKER` | tcp://localhost:1883 | MQTT Broker 地址 |
| `SERVER_PORT` | 8080 | HTTP 服务端口 |
| `PPROF_PORT` | 6060 | pprof 监控端口 |
| `OPTIMIZER_CONFIG` | config/optimizer.json | GA 配置文件路径 |
