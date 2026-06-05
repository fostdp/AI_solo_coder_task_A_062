(function () {
    var API_BASE = window.location.origin;
    var WS_URL = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') + window.location.host + '/ws';

    var state = {
        boreholes: [],
        pipelines: [],
        pumpStations: [],
        alerts: [],
        kpi: { total_flow: 0, avg_concentration: 0, pump_efficiency: 0 },
        optimization: null,
        selectedBorehole: null,
        camera: { x: 0, y: 0, zoom: 1 },
        isDragging: false,
        dragStart: { x: 0, y: 0 },
        camStart: { x: 0, y: 0 },
        ws: null
    };

    var canvas, ctx;
    var boreholeScreenCache = [];

    function init() {
        canvas = document.getElementById('network-canvas');
        ctx = canvas.getContext('2d');
        resizeCanvas();
        window.addEventListener('resize', resizeCanvas);
        setupCanvasInteraction();
        setupUI();
        connectWebSocket();
        fetchAllData();
    }

    function resizeCanvas() {
        var container = document.getElementById('canvas-container');
        canvas.width = container.clientWidth * window.devicePixelRatio;
        canvas.height = container.clientHeight * window.devicePixelRatio;
        canvas.style.width = container.clientWidth + 'px';
        canvas.style.height = container.clientHeight + 'px';
        ctx.setTransform(window.devicePixelRatio, 0, 0, window.devicePixelRatio, 0, 0);
        render();
    }

    function toScreen(lng, lat) {
        var bounds = getDataBounds();
        var cw = canvas.width / window.devicePixelRatio;
        var ch = canvas.height / window.devicePixelRatio;
        var padding = 60;
        var scaleX = (cw - padding * 2) / (bounds.maxLng - bounds.minLng || 0.001);
        var scaleY = (ch - padding * 2) / (bounds.maxLat - bounds.minLat || 0.001);
        var scale = Math.min(scaleX, scaleY);
        var sx = padding + (lng - bounds.minLng) * scale;
        var sy = ch - padding - (lat - bounds.minLat) * scale;
        sx = (sx - cw / 2) * state.camera.zoom + cw / 2 + state.camera.x;
        sy = (sy - ch / 2) * state.camera.zoom + ch / 2 + state.camera.y;
        return { x: sx, y: sy };
    }

    function getDataBounds() {
        if (state.boreholes.length === 0) {
            return { minLng: 106.545, maxLng: 106.565, minLat: 28.625, maxLat: 28.640 };
        }
        var minLng = Infinity, maxLng = -Infinity, minLat = Infinity, maxLat = -Infinity;
        for (var i = 0; i < state.boreholes.length; i++) {
            var b = state.boreholes[i];
            if (b.lng < minLng) minLng = b.lng;
            if (b.lng > maxLng) maxLng = b.lng;
            if (b.lat < minLat) minLat = b.lat;
            if (b.lat > maxLat) maxLat = b.lat;
        }
        for (var i = 0; i < state.pumpStations.length; i++) {
            var p = state.pumpStations[i];
            if (p.lng < minLng) minLng = p.lng;
            if (p.lng > maxLng) maxLng = p.lng;
            if (p.lat < minLat) minLat = p.lat;
            if (p.lat > maxLat) maxLat = p.lat;
        }
        return { minLng: minLng - 0.001, maxLng: maxLng + 0.001, minLat: minLat - 0.001, maxLat: maxLat + 0.001 };
    }

    function scoreColor(score) {
        if (score >= 0.6) return '#00e676';
        if (score >= 0.3) return '#ffc107';
        return '#f44336';
    }

    function scoreGlow(score) {
        if (score >= 0.6) return 'rgba(0,230,118,0.3)';
        if (score >= 0.3) return 'rgba(255,193,7,0.3)';
        return 'rgba(244,67,54,0.3)';
    }

    function render() {
        var cw = canvas.width / window.devicePixelRatio;
        var ch = canvas.height / window.devicePixelRatio;
        ctx.clearRect(0, 0, cw, ch);
        drawBackground(cw, ch);
        drawPipelines();
        drawArrows();
        drawBoreholes();
        drawPumpStations();
        drawLegend(cw, ch);
    }

    function drawBackground(cw, ch) {
        ctx.fillStyle = '#0a0e1a';
        ctx.fillRect(0, 0, cw, ch);
        ctx.strokeStyle = 'rgba(30,58,95,0.3)';
        ctx.lineWidth = 0.5;
        var step = 50 * state.camera.zoom;
        if (step < 10) step = 10;
        for (var x = state.camera.x % step; x < cw; x += step) {
            ctx.beginPath();
            ctx.moveTo(x, 0);
            ctx.lineTo(x, ch);
            ctx.stroke();
        }
        for (var y = state.camera.y % step; y < ch; y += step) {
            ctx.beginPath();
            ctx.moveTo(0, y);
            ctx.lineTo(cw, y);
            ctx.stroke();
        }
    }

    function drawPipelines() {
        for (var i = 0; i < state.pipelines.length; i++) {
            var p = state.pipelines[i];
            if (!p.points || p.points.length < 2) continue;
            ctx.beginPath();
            ctx.strokeStyle = 'rgba(79,195,247,0.25)';
            ctx.lineWidth = 3 * state.camera.zoom;
            var sp = toScreen(p.points[0][0], p.points[0][1]);
            ctx.moveTo(sp.x, sp.y);
            for (var j = 1; j < p.points.length; j++) {
                sp = toScreen(p.points[j][0], p.points[j][1]);
                ctx.lineTo(sp.x, sp.y);
            }
            ctx.stroke();
        }
        for (var si = 0; si < state.pumpStations.length; si++) {
            var station = state.pumpStations[si];
            var stationPos = toScreen(station.lng, station.lat);
            var stationBoreholes = state.boreholes.filter(function (b) { return b.pump_station_id === station.id; });
            for (var bi = 0; bi < stationBoreholes.length; bi++) {
                var bh = stationBoreholes[bi];
                var bp = toScreen(bh.lng, bh.lat);
                var score = bh.score || 0;
                var color = scoreColor(score);
                ctx.beginPath();
                ctx.strokeStyle = color.replace(')', ',0.15)').replace('rgb', 'rgba').replace('##', '#');
                var alpha = 0.12;
                if (score >= 0.6) alpha = 0.15;
                else if (score >= 0.3) alpha = 0.12;
                else alpha = 0.08;
                ctx.strokeStyle = 'rgba(100,150,200,' + alpha + ')';
                ctx.lineWidth = 1 * state.camera.zoom;
                ctx.moveTo(stationPos.x, stationPos.y);
                ctx.lineTo(bp.x, bp.y);
                ctx.stroke();
            }
        }
    }

    function drawBoreholes() {
        boreholeScreenCache = [];
        var baseR = 4 * state.camera.zoom;
        for (var i = 0; i < state.boreholes.length; i++) {
            var b = state.boreholes[i];
            var sp = toScreen(b.lng, b.lat);
            var score = b.score || 0;
            var color = scoreColor(score);
            var glow = scoreGlow(score);
            var r = baseR + score * 2 * state.camera.zoom;
            ctx.beginPath();
            ctx.arc(sp.x, sp.y, r + 3 * state.camera.zoom, 0, Math.PI * 2);
            ctx.fillStyle = glow;
            ctx.fill();
            ctx.beginPath();
            ctx.arc(sp.x, sp.y, r, 0, Math.PI * 2);
            ctx.fillStyle = color;
            ctx.fill();
            if (state.selectedBorehole && state.selectedBorehole.id === b.id) {
                ctx.beginPath();
                ctx.arc(sp.x, sp.y, r + 6 * state.camera.zoom, 0, Math.PI * 2);
                ctx.strokeStyle = '#4fc3f7';
                ctx.lineWidth = 2;
                ctx.stroke();
            }
            boreholeScreenCache.push({ x: sp.x, y: sp.y, r: r + 3, borehole: b });
        }
    }

    function drawPumpStations() {
        var size = 14 * state.camera.zoom;
        for (var i = 0; i < state.pumpStations.length; i++) {
            var p = state.pumpStations[i];
            var sp = toScreen(p.lng, p.lat);
            ctx.beginPath();
            ctx.arc(sp.x, sp.y, size + 4 * state.camera.zoom, 0, Math.PI * 2);
            ctx.fillStyle = 'rgba(79,195,247,0.15)';
            ctx.fill();
            ctx.beginPath();
            ctx.rect(sp.x - size / 2, sp.y - size / 2, size, size);
            ctx.fillStyle = '#1565c0';
            ctx.fill();
            ctx.strokeStyle = '#4fc3f7';
            ctx.lineWidth = 1.5;
            ctx.stroke();
            ctx.fillStyle = '#4fc3f7';
            ctx.font = (11 * state.camera.zoom) + 'px sans-serif';
            ctx.textAlign = 'center';
            ctx.fillText(p.name, sp.x, sp.y - size / 2 - 6 * state.camera.zoom);
        }
    }

    function drawArrows() {
        if (!state.optimization || !state.optimization.recommendations) return;
        var recs = state.optimization.recommendations;
        for (var i = 0; i < recs.length; i++) {
            var rec = recs[i];
            if (rec.target_type === 'borehole') {
                var bh = state.boreholes.find(function (b) { return b.id === rec.target_id; });
                if (!bh) continue;
                var sp = toScreen(bh.lng, bh.lat);
                var dir = rec.command_value > (bh.valve_opening || 50) ? 1 : -1;
                drawArrow(ctx, sp.x, sp.y - 15, sp.x + dir * 20, sp.y - 15, '#4fc3f7');
            } else if (rec.target_type === 'pump_station') {
                var ps = state.pumpStations.find(function (p) { return p.id === rec.target_id; });
                if (!ps) continue;
                var sp = toScreen(ps.lng, ps.lat);
                var dir = rec.command_value > (ps.negative_pressure || 40) ? 1 : -1;
                drawArrow(ctx, sp.x, sp.y - 22, sp.x + dir * 25, sp.y - 22, '#00e676');
            }
        }
    }

    function drawArrow(c, fromX, fromY, toX, toY, color) {
        var headLen = 8;
        var angle = Math.atan2(toY - fromY, toX - fromX);
        c.beginPath();
        c.moveTo(fromX, fromY);
        c.lineTo(toX, toY);
        c.strokeStyle = color;
        c.lineWidth = 2;
        c.stroke();
        c.beginPath();
        c.moveTo(toX, toY);
        c.lineTo(toX - headLen * Math.cos(angle - Math.PI / 6), toY - headLen * Math.sin(angle - Math.PI / 6));
        c.lineTo(toX - headLen * Math.cos(angle + Math.PI / 6), toY - headLen * Math.sin(angle + Math.PI / 6));
        c.closePath();
        c.fillStyle = color;
        c.fill();
    }

    function drawLegend(cw, ch) {
        var existing = document.querySelector('.legend');
        if (existing) existing.remove();
        var div = document.createElement('div');
        div.className = 'legend';
        div.innerHTML = '<div class="legend-item"><div class="legend-dot green"></div><span>高效抽采 (≥60%)</span></div>' +
            '<div class="legend-item"><div class="legend-dot yellow"></div><span>中等抽采 (30-60%)</span></div>' +
            '<div class="legend-item"><div class="legend-dot red"></div><span>低效抽采 (&lt;30%)</span></div>';
        document.body.appendChild(div);
    }

    function setupCanvasInteraction() {
        canvas.addEventListener('mousedown', function (e) {
            state.isDragging = true;
            state.dragStart = { x: e.clientX, y: e.clientY };
            state.camStart = { x: state.camera.x, y: state.camera.y };
        });
        canvas.addEventListener('mousemove', function (e) {
            if (state.isDragging) {
                state.camera.x = state.camStart.x + (e.clientX - state.dragStart.x);
                state.camera.y = state.camStart.y + (e.clientY - state.dragStart.y);
                render();
            }
        });
        canvas.addEventListener('mouseup', function (e) {
            if (state.isDragging) {
                var dx = Math.abs(e.clientX - state.dragStart.x);
                var dy = Math.abs(e.clientY - state.dragStart.y);
                if (dx < 5 && dy < 5) {
                    handleCanvasClick(e);
                }
            }
            state.isDragging = false;
        });
        canvas.addEventListener('wheel', function (e) {
            e.preventDefault();
            var zoomFactor = e.deltaY < 0 ? 1.1 : 0.9;
            state.camera.zoom = Math.max(0.3, Math.min(5, state.camera.zoom * zoomFactor));
            render();
        });
    }

    function handleCanvasClick(e) {
        var rect = canvas.getBoundingClientRect();
        var mx = e.clientX - rect.left;
        var my = e.clientY - rect.top;
        for (var i = 0; i < boreholeScreenCache.length; i++) {
            var bc = boreholeScreenCache[i];
            var dx = mx - bc.x;
            var dy = my - bc.y;
            if (dx * dx + dy * dy <= (bc.r + 4) * (bc.r + 4)) {
                selectBorehole(bc.borehole);
                return;
            }
        }
        closeSidebar();
    }

    function selectBorehole(bh) {
        state.selectedBorehole = bh;
        render();
        var sidebar = document.getElementById('sidebar');
        sidebar.classList.remove('hidden');
        document.getElementById('sidebar-title').textContent = bh.name;
        var info = document.getElementById('borehole-info');
        var score = (bh.score || 0) * 100;
        var scoreColorVal = scoreColor(bh.score || 0);
        info.innerHTML =
            '<div class="info-item"><span class="label">综合评分</span><span class="value" style="color:' + scoreColorVal + '">' + score.toFixed(1) + '%</span></div>' +
            '<div class="info-item"><span class="label">瓦斯流量</span><span class="value">' + (bh.gas_flow || 0).toFixed(2) + ' m³/min</span></div>' +
            '<div class="info-item"><span class="label">瓦斯浓度</span><span class="value">' + (bh.gas_concentration || 0).toFixed(1) + '%</span></div>' +
            '<div class="info-item"><span class="label">负压</span><span class="value">' + (bh.negative_pressure || 0).toFixed(1) + ' kPa</span></div>' +
            '<div class="info-item"><span class="label">温度</span><span class="value">' + (bh.temperature || 0).toFixed(1) + ' °C</span></div>' +
            '<div class="info-item"><span class="label">阀门开度</span><span class="value">' + (bh.valve_opening || 0).toFixed(0) + '%</span></div>';
        fetchBoreholeTrend(bh.id);
    }

    function closeSidebar() {
        state.selectedBorehole = null;
        document.getElementById('sidebar').classList.add('hidden');
        render();
    }

    function fetchBoreholeTrend(id) {
        fetch(API_BASE + '/api/boreholes/' + id + '/trend')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                drawTrendChart('flow-chart', data.flow || [], '#4fc3f7', 'm³/min');
                drawTrendChart('conc-chart', data.concentration || [], '#00e676', '%');
            })
            .catch(function () { });
    }

    function drawTrendChart(canvasId, data, color, unit) {
        var c = document.getElementById(canvasId);
        var cctx = c.getContext('2d');
        var dpr = window.devicePixelRatio;
        c.width = c.clientWidth * dpr;
        c.height = c.clientHeight * dpr;
        cctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        var w = c.clientWidth;
        var h = c.clientHeight;
        var pad = { top: 10, right: 10, bottom: 20, left: 40 };
        var plotW = w - pad.left - pad.right;
        var plotH = h - pad.top - pad.bottom;
        cctx.clearRect(0, 0, w, h);
        if (data.length === 0) return;
        var minV = Math.min.apply(null, data);
        var maxV = Math.max.apply(null, data);
        if (maxV === minV) { maxV += 1; minV -= 1; }
        cctx.strokeStyle = 'rgba(30,58,95,0.5)';
        cctx.lineWidth = 0.5;
        for (var gi = 0; gi < 4; gi++) {
            var gy = pad.top + plotH * gi / 3;
            cctx.beginPath();
            cctx.moveTo(pad.left, gy);
            cctx.lineTo(w - pad.right, gy);
            cctx.stroke();
            var val = maxV - (maxV - minV) * gi / 3;
            cctx.fillStyle = '#667788';
            cctx.font = '10px sans-serif';
            cctx.textAlign = 'right';
            cctx.fillText(val.toFixed(1), pad.left - 4, gy + 3);
        }
        cctx.beginPath();
        cctx.strokeStyle = color;
        cctx.lineWidth = 1.5;
        for (var i = 0; i < data.length; i++) {
            var x = pad.left + (i / (data.length - 1)) * plotW;
            var y = pad.top + (1 - (data[i] - minV) / (maxV - minV)) * plotH;
            if (i === 0) cctx.moveTo(x, y);
            else cctx.lineTo(x, y);
        }
        cctx.stroke();
        var grad = cctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
        grad.addColorStop(0, color.replace(')', ',0.2)').replace('#', 'rgba(').replace('rgba(#', ''));
        var rgbaColor = hexToRgba(color, 0.05);
        grad.addColorStop(1, rgbaColor);
        cctx.lineTo(pad.left + plotW, pad.top + plotH);
        cctx.lineTo(pad.left, pad.top + plotH);
        cctx.closePath();
        cctx.fillStyle = grad;
        cctx.fill();
    }

    function hexToRgba(hex, alpha) {
        var r = parseInt(hex.slice(1, 3), 16);
        var g = parseInt(hex.slice(3, 5), 16);
        var b = parseInt(hex.slice(5, 7), 16);
        return 'rgba(' + r + ',' + g + ',' + b + ',' + alpha + ')';
    }

    function setupUI() {
        document.getElementById('sidebar-close').addEventListener('click', closeSidebar);
        document.getElementById('btn-optimize').addEventListener('click', function () {
            fetch(API_BASE + '/api/optimization/run', { method: 'POST' })
                .then(function (r) { return r.json(); })
                .then(function (data) {
                    state.optimization = data;
                    showOptimizationPanel(data);
                })
                .catch(function () { alert('优化计算请求失败'); });
        });
        document.getElementById('btn-alerts').addEventListener('click', function () {
            var list = document.getElementById('alert-list');
            list.style.display = list.style.display === 'none' ? 'block' : 'none';
        });
        document.getElementById('alert-toggle').addEventListener('click', function () {
            var list = document.getElementById('alert-list');
            list.style.display = list.style.display === 'none' ? 'block' : 'none';
        });
        document.getElementById('opt-close').addEventListener('click', function () {
            document.getElementById('optimization-panel').classList.add('hidden');
        });
        document.getElementById('opt-apply-btn').addEventListener('click', function () {
            alert('调控指令已下发至PLC，等待执行反馈...');
        });
    }

    function showOptimizationPanel(data) {
        var panel = document.getElementById('optimization-panel');
        panel.classList.remove('hidden');
        var content = document.getElementById('opt-content');
        var html = '';
        if (data.recommendations) {
            var stations = {};
            for (var i = 0; i < data.recommendations.length; i++) {
                var rec = data.recommendations[i];
                if (!stations[rec.target_type + '_' + rec.target_id]) {
                    stations[rec.target_type + '_' + rec.target_id] = [];
                }
                stations[rec.target_type + '_' + rec.target_id].push(rec);
            }
            var keys = Object.keys(stations);
            for (var ki = 0; ki < keys.length; ki++) {
                var recs = stations[keys[ki]];
                var title = recs[0].target_type === 'pump_station' ? '泵站 #' + recs[0].target_id : '钻孔 #' + recs[0].target_id;
                html += '<div class="opt-station"><h4>' + title + '</h4>';
                for (var ri = 0; ri < recs.length; ri++) {
                    html += '<div class="opt-row"><span>' + recs[ri].command_type + '</span>' +
                        '<span><span class="old-val">' + (recs[ri].current_value || '--').toFixed(1) + '</span>' +
                        '<span class="opt-arrow"> → </span>' +
                        '<span class="new-val">' + recs[ri].command_value.toFixed(1) + '</span></span></div>';
                }
                html += '</div>';
            }
        }
        html += '<div style="text-align:center;color:#8899aa;font-size:12px;margin-top:8px;">预期总管浓度: ' +
            (data.total_concentration || 0).toFixed(2) + '%</div>';
        content.innerHTML = html;
        render();
    }

    function fetchAllData() {
        fetch(API_BASE + '/api/boreholes')
            .then(function (r) { return r.json(); })
            .then(function (d) { state.boreholes = d || []; render(); })
            .catch(function () { });
        fetch(API_BASE + '/api/pipelines')
            .then(function (r) { return r.json(); })
            .then(function (d) { state.pipelines = d || []; render(); })
            .catch(function () { });
        fetch(API_BASE + '/api/pump-stations')
            .then(function (r) { return r.json(); })
            .then(function (d) { state.pumpStations = d || []; render(); })
            .catch(function () { });
        fetchKPI();
        fetchAlerts();
        fetch(API_BASE + '/api/optimization/latest')
            .then(function (r) { return r.json(); })
            .then(function (d) { if (d && d.id) { state.optimization = d; render(); } })
            .catch(function () { });
    }

    function fetchKPI() {
        fetch(API_BASE + '/api/kpi')
            .then(function (r) { return r.json(); })
            .then(function (d) {
                state.kpi = d || {};
                document.getElementById('kpi-total-flow').textContent = (d.total_flow || 0).toFixed(1);
                document.getElementById('kpi-avg-conc').textContent = (d.avg_concentration || 0).toFixed(1);
                document.getElementById('kpi-pump-eff').textContent = (d.pump_efficiency || 0).toFixed(1);
            })
            .catch(function () { });
    }

    function fetchAlerts() {
        fetch(API_BASE + '/api/alerts')
            .then(function (r) { return r.json(); })
            .then(function (d) {
                state.alerts = d || [];
                renderAlerts();
            })
            .catch(function () { });
    }

    function renderAlerts() {
        var list = document.getElementById('alert-list');
        var count = document.getElementById('alert-count');
        count.textContent = state.alerts.length;
        var html = '';
        for (var i = 0; i < state.alerts.length; i++) {
            var a = state.alerts[i];
            html += '<div class="alert-item ' + a.level + '">' +
                '<div>' + a.message + '</div>' +
                '<div class="alert-time">' + (a.created_at || '') + '</div></div>';
        }
        list.innerHTML = html;
    }

    function connectWebSocket() {
        state.ws = new WebSocket(WS_URL);
        state.ws.onopen = function () { console.log('WS connected'); };
        state.ws.onmessage = function (evt) {
            try {
                var msg = JSON.parse(evt.data);
                handleWSMessage(msg);
            } catch (e) { }
        };
        state.ws.onclose = function () {
            setTimeout(connectWebSocket, 3000);
        };
        state.ws.onerror = function () { state.ws.close(); };
    }

    function handleWSMessage(msg) {
        if (msg.type === 'borehole_update') {
            for (var i = 0; i < state.boreholes.length; i++) {
                if (state.boreholes[i].id === msg.data.borehole_id) {
                    state.boreholes[i].gas_flow = msg.data.gas_flow;
                    state.boreholes[i].gas_concentration = msg.data.gas_concentration;
                    state.boreholes[i].negative_pressure = msg.data.negative_pressure;
                    state.boreholes[i].temperature = msg.data.temperature;
                    state.boreholes[i].score = computeScore(msg.data.gas_concentration, msg.data.gas_flow);
                    break;
                }
            }
            render();
            fetchKPI();
        } else if (msg.type === 'alert') {
            state.alerts.unshift(msg.data);
            renderAlerts();
        } else if (msg.type === 'optimization') {
            state.optimization = msg.data;
            showOptimizationPanel(msg.data);
            render();
        } else if (msg.type === 'kpi_update') {
            state.kpi = msg.data;
            document.getElementById('kpi-total-flow').textContent = (msg.data.total_flow || 0).toFixed(1);
            document.getElementById('kpi-avg-conc').textContent = (msg.data.avg_concentration || 0).toFixed(1);
            document.getElementById('kpi-pump-eff').textContent = (msg.data.pump_efficiency || 0).toFixed(1);
        }
    }

    function computeScore(conc, flow) {
        var s = (conc / 70.0) * 0.6 + (flow / 5.0) * 0.4;
        if (s < 0) s = 0;
        if (s > 1) s = 1;
        return s;
    }

    document.addEventListener('DOMContentLoaded', init);
})();
