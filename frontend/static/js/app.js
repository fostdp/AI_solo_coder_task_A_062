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
        ws: null,
        onBoreholeClick: function (bh) { BoreholeDetail.selectBorehole(bh); },
        onBackgroundClick: function () { BoreholeDetail.closeSidebar(); }
    };

    function init() {
        PipeNetwork.init(state);
        BoreholeDetail.init(state);
        setupUI();
        connectWebSocket();
        fetchAllData();
    }

    function setupUI() {
        document.getElementById('btn-optimize').addEventListener('click', function () {
            fetch(API_BASE + '/api/optimization/run', { method: 'POST' })
                .then(function (r) { return r.json(); })
                .then(function (data) {
                    if (data.status === 'running') {
                        BoreholeDetail.showOptimizationPanel({ total_concentration: 0, recommendations: null, message: data.message });
                    } else {
                        BoreholeDetail.showOptimizationPanel(data);
                    }
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

    function fetchAllData() {
        fetch(API_BASE + '/api/boreholes')
            .then(function (r) { return r.json(); })
            .then(function (d) { state.boreholes = d || []; PipeNetwork.render(); })
            .catch(function () { });
        fetch(API_BASE + '/api/pipelines')
            .then(function (r) { return r.json(); })
            .then(function (d) { state.pipelines = d || []; PipeNetwork.render(); })
            .catch(function () { });
        fetch(API_BASE + '/api/pump-stations')
            .then(function (r) { return r.json(); })
            .then(function (d) { state.pumpStations = d || []; PipeNetwork.render(); })
            .catch(function () { });
        fetchKPI();
        fetchAlerts();
        fetch(API_BASE + '/api/optimization/latest')
            .then(function (r) { return r.json(); })
            .then(function (d) { if (d && d.id) { state.optimization = d; PipeNetwork.render(); } })
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
            PipeNetwork.render();
            fetchKPI();
        } else if (msg.type === 'alert') {
            state.alerts.unshift(msg.data);
            renderAlerts();
        } else if (msg.type === 'optimization') {
            state.optimization = msg.data;
            BoreholeDetail.showOptimizationPanel(msg.data);
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
