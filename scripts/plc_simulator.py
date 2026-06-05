import json
import time
import random
import argparse
import sys

try:
    import paho.mqtt.client as mqtt
except ImportError:
    print("paho-mqtt not installed. Run: pip install paho-mqtt")
    sys.exit(1)


BROKER = "localhost"
PORT = 1883


class PLCSimulator:
    def __init__(self, broker, port, client_id="plc-simulator"):
        self.broker = broker
        self.port = port
        self.client_id = client_id
        self.client = mqtt.Client(client_id=client_id)
        self.client.on_connect = self._on_connect
        self.client.on_message = self._on_message
        self.current_state = {}

    def _on_connect(self, client, userdata, flags, rc):
        if rc == 0:
            print("Connected to MQTT broker")
            client.subscribe("gas/plc/+/+/command")
            print("Subscribed to gas/plc/+/+/command")
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
            time.sleep(random.uniform(0.5, 2.0))
            success = random.random() > 0.05
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
    parser.add_argument("--broker", default="localhost", help="MQTT broker address")
    parser.add_argument("--port", type=int, default=1883, help="MQTT broker port")
    args = parser.parse_args()
    sim = PLCSimulator(args.broker, args.port)
    try:
        sim.start()
    except KeyboardInterrupt:
        print("\nPLC Simulator stopped")
        sys.exit(0)


if __name__ == "__main__":
    main()
