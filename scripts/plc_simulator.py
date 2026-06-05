import json
import time
import random
import argparse
import sys
import os

try:
    import paho.mqtt.client as mqtt
except ImportError:
    print("paho-mqtt not installed. Run: pip install paho-mqtt")
    sys.exit(1)


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


class PLCSimulator:
    def __init__(self, broker, port, client_id="plc-simulator",
                 fail_rate=0.05, delay_min=0.5, delay_max=2.0):
        self.broker = broker
        self.port = port
        self.client_id = client_id
        self.fail_rate = fail_rate
        self.delay_min = delay_min
        self.delay_max = delay_max
        self.client = mqtt.Client(client_id=client_id, clean_session=False)
        self.client.on_connect = self._on_connect
        self.client.on_message = self._on_message
        self.current_state = {}
        will_topic = "gas/plc/simulator/status"
        will_payload = json.dumps({"client_id": client_id, "status": "offline", "timestamp": time.time()})
        self.client.will_set(will_topic, will_payload, qos=1, retain=True)

    def _on_connect(self, client, userdata, flags, rc):
        if rc == 0:
            print("Connected to MQTT broker")
            client.subscribe("gas/plc/+/+/command", qos=1)
            print("Subscribed to gas/plc/+/+/command")
            status_topic = "gas/plc/simulator/status"
            client.publish(status_topic, json.dumps({
                "client_id": self.client_id,
                "status": "online",
                "timestamp": time.time()
            }), qos=1, retain=True)
        else:
            print(f"Connection failed with code {rc}")

    def _on_message(self, client, userdata, msg):
        try:
            payload = json.loads(msg.payload.decode())
            topic_parts = msg.topic.split("/")
            if len(topic_parts) >= 5:
                target_type = topic_parts[2]
                target_id = topic_parts[3]
            else:
                target_type = "unknown"
                target_id = "0"
            print(f"Command received: {msg.topic} -> {json.dumps(payload)}")
            command_type = payload.get("command_type", "set")
            command_value = payload.get("command_value", 0)
            key = f"{target_type}_{target_id}"
            old_val = self.current_state.get(key, command_value)
            self.current_state[key] = command_value
            time.sleep(random.uniform(self.delay_min, self.delay_max))
            success = random.random() > self.fail_rate
            feedback = {
                "target_type": target_type,
                "target_id": int(target_id),
                "command_type": command_type,
                "command_value": command_value,
                "old_value": old_val,
                "success": success,
                "actual_value": command_value + random.uniform(-1, 1) if success else old_val,
                "timestamp": time.time(),
            }
            if not success:
                feedback["error"] = "PLC execution timeout"
            feedback_topic = f"gas/plc/{target_type}/{target_id}/feedback"
            client.publish(feedback_topic, json.dumps(feedback), qos=1)
            status = "OK" if success else "FAILED"
            print(f"  Feedback sent to {feedback_topic}: {status} (value: {feedback['actual_value']:.2f})")
        except Exception as e:
            print(f"Error processing message: {e}")

    def start(self):
        print(f"PLC Simulator: broker={self.broker}:{self.port} fail_rate={self.fail_rate} "
              f"delay={self.delay_min}-{self.delay_max}s")
        print(f"Connecting to MQTT broker at {self.broker}:{self.port}...")
        try:
            self.client.connect(self.broker, self.port, 60)
        except Exception as e:
            print(f"Failed to connect: {e}")
            print("Will retry in 5 seconds...")
            time.sleep(5)
            self.client.connect(self.broker, self.port, 60)
        print("PLC Simulator running. Press Ctrl+C to stop.")
        self.client.loop_forever()


def main():
    parser = argparse.ArgumentParser(description="PLC Simulator for Gas Drainage System")
    parser.add_argument("--broker", default=os.environ.get("MQTT_BROKER", "localhost"),
                        help="MQTT broker address")
    parser.add_argument("--port", type=int, default=env_int("MQTT_PORT", 1883),
                        help="MQTT broker port")
    parser.add_argument("--fail-rate", type=float, default=env_float("FAIL_RATE", 0.05),
                        help="Command execution failure rate (0.0-1.0)")
    parser.add_argument("--delay-min", type=float, default=env_float("DELAY_MIN", 0.5),
                        help="Minimum execution delay (seconds)")
    parser.add_argument("--delay-max", type=float, default=env_float("DELAY_MAX", 2.0),
                        help="Maximum execution delay (seconds)")
    args = parser.parse_args()

    sim = PLCSimulator(
        broker=args.broker,
        port=args.port,
        fail_rate=args.fail_rate,
        delay_min=args.delay_min,
        delay_max=args.delay_max,
    )
    try:
        sim.start()
    except KeyboardInterrupt:
        print("\nPLC Simulator stopped")
        sys.exit(0)


if __name__ == "__main__":
    main()
