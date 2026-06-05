var PipeNetwork = (function () {
    var canvas, ctx;
    var screenCache = [];
    var state = null;

    function init(st) {
        state = st;
        canvas = document.getElementById('network-canvas');
        ctx = canvas.getContext('2d');
        resizeCanvas();
        window.addEventListener('resize', resizeCanvas);
        setupInteraction();
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

    function hexToRgba(hex, alpha) {
        var r = parseInt(hex.slice(1, 3), 16);
        var g = parseInt(hex.slice(3, 5), 16);
        var b = parseInt(hex.slice(5, 7), 16);
        return 'rgba(' + r + ',' + g + ',' + b + ',' + alpha + ')';
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
            ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, ch); ctx.stroke();
        }
        for (var y = state.camera.y % step; y < ch; y += step) {
            ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(cw, y); ctx.stroke();
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
                var alpha = score >= 0.6 ? 0.15 : (score >= 0.3 ? 0.12 : 0.08);
                ctx.beginPath();
                ctx.strokeStyle = 'rgba(100,150,200,' + alpha + ')';
                ctx.lineWidth = 1 * state.camera.zoom;
                ctx.moveTo(stationPos.x, stationPos.y);
                ctx.lineTo(bp.x, bp.y);
                ctx.stroke();
            }
        }
    }

    function drawBoreholes() {
        screenCache = [];
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
            screenCache.push({ x: sp.x, y: sp.y, r: r + 3, borehole: b });
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

    function setupInteraction() {
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
                    handleClick(e);
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

    function handleClick(e) {
        var rect = canvas.getBoundingClientRect();
        var mx = e.clientX - rect.left;
        var my = e.clientY - rect.top;
        for (var i = 0; i < screenCache.length; i++) {
            var bc = screenCache[i];
            var dx = mx - bc.x;
            var dy = my - bc.y;
            if (dx * dx + dy * dy <= (bc.r + 4) * (bc.r + 4)) {
                if (state.onBoreholeClick) state.onBoreholeClick(bc.borehole);
                return;
            }
        }
        if (state.onBackgroundClick) state.onBackgroundClick();
    }

    return {
        init: init,
        render: render,
        scoreColor: scoreColor,
        hexToRgba: hexToRgba
    };
})();
