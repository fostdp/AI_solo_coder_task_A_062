var BoreholeDetail = (function () {
    var API_BASE = window.location.origin;
    var state = null;

    function init(st) {
        state = st;
        document.getElementById('sidebar-close').addEventListener('click', closeSidebar);
    }

    function selectBorehole(bh) {
        state.selectedBorehole = bh;
        PipeNetwork.render();
        var sidebar = document.getElementById('sidebar');
        sidebar.classList.remove('hidden');
        document.getElementById('sidebar-title').textContent = bh.name;
        var info = document.getElementById('borehole-info');
        var score = (bh.score || 0) * 100;
        var scoreColorVal = PipeNetwork.scoreColor(bh.score || 0);
        info.innerHTML =
            '<div class="info-item"><span class="label">综合评分</span><span class="value" style="color:' + scoreColorVal + '">' + score.toFixed(1) + '%</span></div>' +
            '<div class="info-item"><span class="label">瓦斯流量</span><span class="value">' + (bh.gas_flow || 0).toFixed(2) + ' m³/min</span></div>' +
            '<div class="info-item"><span class="label">瓦斯浓度</span><span class="value">' + (bh.gas_concentration || 0).toFixed(1) + '%</span></div>' +
            '<div class="info-item"><span class="label">负压</span><span class="value">' + (bh.negative_pressure || 0).toFixed(1) + ' kPa</span></div>' +
            '<div class="info-item"><span class="label">温度</span><span class="value">' + (bh.temperature || 0).toFixed(1) + ' °C</span></div>' +
            '<div class="info-item"><span class="label">阀门开度</span><span class="value">' + (bh.valve_opening || 0).toFixed(0) + '%</span></div>';
        fetchTrend(bh.id);
    }

    function closeSidebar() {
        state.selectedBorehole = null;
        document.getElementById('sidebar').classList.add('hidden');
        PipeNetwork.render();
    }

    function fetchTrend(id) {
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
        grad.addColorStop(0, PipeNetwork.hexToRgba(color, 0.2));
        grad.addColorStop(1, PipeNetwork.hexToRgba(color, 0.05));
        cctx.lineTo(pad.left + plotW, pad.top + plotH);
        cctx.lineTo(pad.left, pad.top + plotH);
        cctx.closePath();
        cctx.fillStyle = grad;
        cctx.fill();
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
        PipeNetwork.render();
    }

    return {
        init: init,
        selectBorehole: selectBorehole,
        closeSidebar: closeSidebar,
        showOptimizationPanel: showOptimizationPanel
    };
})();
