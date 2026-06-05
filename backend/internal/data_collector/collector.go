package data_collector

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/models"
)

type Broadcaster interface {
	Broadcast(msgType string, payload interface{})
}

type DataEvent struct {
	Kind     string
	Payload  json.RawMessage
	Response chan *DataResponse
}

type DataResponse struct {
	Data  interface{}
	Error error
}

type DataCollector struct {
	db     *database.DB
	hub    Broadcaster
	DataCh chan *DataEvent
}

func NewDataCollector(db *database.DB, hub Broadcaster) *DataCollector {
	return &DataCollector{
		db:     db,
		hub:    hub,
		DataCh: make(chan *DataEvent, 256),
	}
}

func (dc *DataCollector) Start() {
	go func() {
		for event := range dc.DataCh {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

			switch event.Kind {
			case "borehole":
				var data models.BoreholeData
				if err := json.Unmarshal(event.Payload, &data); err != nil {
					log.Printf("unmarshal borehole data: %v", err)
					cancel()
					dc.sendResponse(event.Response, &DataResponse{Error: err})
					continue
				}
				if err := dc.db.StoreBoreholeData(ctx, data); err != nil {
					log.Printf("store borehole data: %v", err)
					cancel()
					dc.sendResponse(event.Response, &DataResponse{Error: err})
					continue
				}
				dc.hub.Broadcast("borehole_update", data)
				dc.sendResponse(event.Response, &DataResponse{Data: data})

			case "borehole_batch":
				var batch []models.BoreholeData
				if err := json.Unmarshal(event.Payload, &batch); err != nil {
					log.Printf("unmarshal borehole batch: %v", err)
					cancel()
					dc.sendResponse(event.Response, &DataResponse{Error: err})
					continue
				}
				if err := dc.db.StoreBoreholeDataBatch(ctx, batch); err != nil {
					log.Printf("store borehole batch: %v", err)
					cancel()
					dc.sendResponse(event.Response, &DataResponse{Error: err})
					continue
				}
				dc.hub.Broadcast("borehole_update", map[string]interface{}{"count": len(batch)})
				dc.sendResponse(event.Response, &DataResponse{Data: batch})

			case "pump_station":
				var data models.PumpStationData
				if err := json.Unmarshal(event.Payload, &data); err != nil {
					log.Printf("unmarshal pump station data: %v", err)
					cancel()
					dc.sendResponse(event.Response, &DataResponse{Error: err})
					continue
				}
				if err := dc.db.StorePumpStationData(ctx, data); err != nil {
					log.Printf("store pump station data: %v", err)
					cancel()
					dc.sendResponse(event.Response, &DataResponse{Error: err})
					continue
				}
				dc.sendResponse(event.Response, &DataResponse{Data: data})
			}

			cancel()
		}
	}()
}

func (dc *DataCollector) sendResponse(ch chan *DataResponse, resp *DataResponse) {
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}
