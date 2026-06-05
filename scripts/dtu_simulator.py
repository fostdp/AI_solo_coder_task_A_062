import json
import time
import random
import requests
import argparse
import sys
from threading import Thread


API_BASE = "http://localhost:8080"


class BoreholeSimulator:
    def __init__(self, api_base, num_boreholes=600, interval=120):
        self.api_base = api_base
        self.num_boreholes = num_boreholes
        self.interval = interval
        self.states = {}
        for i in range(1, num_boreholes + 1):
            base_conc = random.uniform(8, 65)
            base_flow = random.uniform(0.3, 5.0)
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
        conc = s["base_conc"] + s["conc_trend"] * random.uniform(0, 3) + random.gauss(0, 2)
        conc = max(0.5, min(80, conc))
        flow = s["base_flow"] + s["flow_trend"] * random.uniform(0, 0.5) + random.gauss(0, 0.2)
        flow = max(0.05, min(6, flow))
        pressure = s["base_pressure"] + random.gauss(0, 1.5)
        pressure = max(5, min(55, pressure))
        temp = s["base_temp"] + random.gauss(0, 0.5)
        temp = max(10, min(40, temp))
        s["base_conc"] = conc * 0.02 + s["base_conc"] * 0.98
        s["base_flow"] = flow * 0.02 + s["base_flow"] * 0.98
        return {
            "borehole_id": borehole_id,
            "gas_flow": round(flow, 3),
            "gas_concentration": round(conc, 2),
            "negative_pressure": round(pressure, 2),
            "temperature": round(temp, 1),
        }

    def send_batch(self, readings):
        url = self.api_base + "/api/data/borehole"
        for r in readings:
            try:
                resp = requests.post(url, json=r, timeout=5)
                if resp.status_code != 200:
                    print(f"  WARN: borehole {r['borehole_id']} status {resp.status_code}")
            except requests.RequestException as e:
                print(f"  ERR: borehole {r['borehole_id']} - {e}")

    def run(self):
        print(f"DTU Simulator started: {self.num_boreholes} boreholes, interval {self.interval}s")
        batch_size = 50
        while True:
            start = time.time()
            readings = [self.generate_reading(i) for i in range(1, self.num_boreholes + 1)]
            threads = []
            for i in range(0, len(readings), batch_size):
                batch = readings[i:i + batch_size]
                t = Thread(target=self.send_batch, args=(batch,))
                threads.append(t)
                t.start()
            for t in threads:
                t.join()
            elapsed = time.time() - start
            print(f"Sent {len(readings)} readings in {elapsed:.1f}s")
            wait = max(1, self.interval - elapsed)
            time.sleep(wait)


def main():
    parser = argparse.ArgumentParser(description="4G DTU Borehole Data Simulator")
    parser.add_argument("--api", default="http://localhost:8080", help="Backend API URL")
    parser.add_argument("--boreholes", type=int, default=600, help="Number of boreholes")
    parser.add_argument("--interval", type=int, default=120, help="Reporting interval in seconds")
    args = parser.parse_args()
    sim = BoreholeSimulator(args.api, args.boreholes, args.interval)
    try:
        sim.run()
    except KeyboardInterrupt:
        print("\nSimulator stopped")
        sys.exit(0)


if __name__ == "__main__":
    main()
