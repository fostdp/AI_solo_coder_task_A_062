import json
import time
import random
import requests
import argparse
import sys
import os


API_BASE = os.environ.get("API_BASE", "http://localhost:8080")


class BoreholeSimulator:
    def __init__(self, api_base, num_boreholes=600, interval=120, use_batch=True,
                 conc_range=(8.0, 65.0), flow_range=(0.3, 5.0),
                 noise_conc=2.0, noise_flow=0.2):
        self.api_base = api_base
        self.num_boreholes = num_boreholes
        self.interval = interval
        self.use_batch = use_batch
        self.noise_conc = noise_conc
        self.noise_flow = noise_flow
        self.states = {}
        for i in range(1, num_boreholes + 1):
            base_conc = random.uniform(conc_range[0], conc_range[1])
            base_flow = random.uniform(flow_range[0], flow_range[1])
            base_pressure = random.uniform(18, 45)
            base_temp = random.uniform(16, 30)
            self.states[i] = {
                "base_conc": base_conc,
                "base_flow": base_flow,
                "base_pressure": base_pressure,
                "base_temp": base_temp,
                "conc_trend": random.choice([-0.5, -0.2, 0, 0.2, 0.5]),
                "flow_trend": random.choice([-0.1, 0, 0.1, 0.2]),
            }

    def generate_reading(self, borehole_id):
        s = self.states[borehole_id]
        conc = s["base_conc"] + s["conc_trend"] * random.uniform(0, 3) + random.gauss(0, self.noise_conc)
        conc = max(0.5, min(80, conc))
        flow = s["base_flow"] + s["flow_trend"] * random.uniform(0, 0.5) + random.gauss(0, self.noise_flow)
        flow = max(0.05, min(6, flow))
        pressure = s["base_pressure"] + random.gauss(0, 1.5)
        pressure = max(5, min(55, pressure))
        temp = s["base_temp"] + random.gauss(0, 0.5)
        temp = max(10, min(40, temp))
        s["base_conc"] = conc * 0.02 + s["base_conc"] * 0.98
        s["base_flow"] = flow * 0.02 + s["base_flow"] * 0.98
        reading = {
            "borehole_id": borehole_id,
            "gas_flow": round(flow, 3),
            "gas_concentration": round(conc, 2),
            "negative_pressure": round(pressure, 2),
            "temperature": round(temp, 1),
        }
        if not self.use_batch:
            from datetime import datetime, timezone
            reading["recorded_at"] = datetime.now(timezone.utc).isoformat()
        return reading

    def send_batch_api(self, readings):
        url = self.api_base + "/api/data/borehole/batch"
        try:
            resp = requests.post(url, json=readings, timeout=15)
            if resp.status_code not in (200, 201):
                print(f"  WARN: batch API status {resp.status_code}, falling back to individual")
                self.send_individual(readings)
            else:
                data = resp.json()
                print(f"  Batch sent: {data.get('count', len(readings))} readings")
        except requests.RequestException as e:
            print(f"  ERR: batch API failed: {e}, falling back to individual")
            self.send_individual(readings)

    def send_individual(self, readings):
        url = self.api_base + "/api/data/borehole"
        for r in readings:
            try:
                resp = requests.post(url, json=r, timeout=5)
                if resp.status_code not in (200, 201):
                    print(f"  WARN: borehole {r['borehole_id']} status {resp.status_code}")
            except requests.RequestException as e:
                print(f"  ERR: borehole {r['borehole_id']} - {e}")

    def run(self):
        mode = "batch" if self.use_batch else "individual"
        print(f"DTU Simulator started: {self.num_boreholes} boreholes, interval {self.interval}s, mode={mode}")
        chunk_size = 100
        while True:
            start = time.time()
            readings = [self.generate_reading(i) for i in range(1, self.num_boreholes + 1)]
            for i in range(0, len(readings), chunk_size):
                chunk = readings[i:i + chunk_size]
                if self.use_batch:
                    self.send_batch_api(chunk)
                else:
                    self.send_individual(chunk)
            elapsed = time.time() - start
            print(f"Sent {len(readings)} readings in {elapsed:.1f}s")
            wait = max(1, self.interval - elapsed)
            time.sleep(wait)


def env_float(key, default):
    v = os.environ.get(key)
    if v:
        try:
            return float(v)
        except ValueError:
            pass
    return default


def env_int(key, default):
    v = os.environ.get(key)
    if v:
        try:
            return int(v)
        except ValueError:
            pass
    return default


def main():
    parser = argparse.ArgumentParser(description="4G DTU Borehole Data Simulator")
    parser.add_argument("--api", default=os.environ.get("API_BASE", "http://localhost:8080"),
                        help="Backend API URL")
    parser.add_argument("--boreholes", type=int, default=env_int("BOREHOLES", 600),
                        help="Number of boreholes")
    parser.add_argument("--interval", type=int, default=env_int("INTERVAL", 120),
                        help="Reporting interval in seconds")
    parser.add_argument("--conc-min", type=float, default=env_float("CONC_MIN", 8.0),
                        help="Minimum base gas concentration (%)")
    parser.add_argument("--conc-max", type=float, default=env_float("CONC_MAX", 65.0),
                        help="Maximum base gas concentration (%)")
    parser.add_argument("--flow-min", type=float, default=env_float("FLOW_MIN", 0.3),
                        help="Minimum base gas flow (m³/min)")
    parser.add_argument("--flow-max", type=float, default=env_float("FLOW_MAX", 5.0),
                        help="Maximum base gas flow (m³/min)")
    parser.add_argument("--noise-conc", type=float, default=env_float("NOISE_CONC", 2.0),
                        help="Concentration Gaussian noise std dev")
    parser.add_argument("--noise-flow", type=float, default=env_float("NOISE_FLOW", 0.2),
                        help="Flow Gaussian noise std dev")
    parser.add_argument("--no-batch", action="store_true",
                        help="Disable batch API, use individual POST")
    args = parser.parse_args()

    sim = BoreholeSimulator(
        api_base=args.api,
        num_boreholes=args.boreholes,
        interval=args.interval,
        use_batch=not args.no_batch,
        conc_range=(args.conc_min, args.conc_max),
        flow_range=(args.flow_min, args.flow_max),
        noise_conc=args.noise_conc,
        noise_flow=args.noise_flow,
    )
    try:
        sim.run()
    except KeyboardInterrupt:
        print("\nSimulator stopped")
        sys.exit(0)


if __name__ == "__main__":
    main()
