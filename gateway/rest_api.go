package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SMSSubmitRequest is the JSON body for POST /api/v1/sms.
type SMSSubmitRequest struct {
	To                 string `json:"to"`
	From               string `json:"from"`
	Body               string `json:"body"`
	Encoding           string `json:"encoding"`               // gsm7 | ucs2 | auto (default auto)
	CallbackURL        string `json:"callback_url,omitempty"`  // per-message DLR callback
	Reference          string `json:"reference,omitempty"`     // client reference
	RegisteredDelivery *bool  `json:"registered_delivery"`     // default true
}

// SMSSubmitResponse is returned for each submitted message.
type SMSSubmitResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	To        string `json:"to"`
	Reference string `json:"reference,omitempty"`
}

// SMSBatchRequest wraps multiple messages.
type SMSBatchRequest struct {
	Messages        []SMSSubmitRequest `json:"messages"`
	CallbackURL     string             `json:"callback_url,omitempty"`
	ReferencePrefix string             `json:"reference_prefix,omitempty"`
}

// HandleHTTPSubmit handles POST /api/v1/sms
func (r *Router) HandleHTTPSubmit(w http.ResponseWriter, req *http.Request) {
	var body SMSSubmitRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.To == "" || body.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to and body are required"})
		return
	}

	// Generate gateway message ID
	gwMsgID := r.nextMsgID()

	// Build submit_sm PDU body
	rawBody := BuildSubmitSMBody(body.From, body.To, []byte(body.Body))

	// Store correlation
	corr := &correlation{
		GwMsgID:     gwMsgID,
		NorthConnID: "rest-api", // Special marker for REST submissions
		MSISDN:      body.To,
		SubmittedAt: time.Now(),
	}
	r.gwCorrelation.Set(gwMsgID, corr)

	// Store submit record in Pebble
	if r.store != nil {
		_ = r.store.StoreSubmit(&SubmitRecord{
			GwMsgID:     gwMsgID,
			NorthConnID: "rest-api",
			MSISDN:      body.To,
			SourceAddr:  body.From,
			Payload:     rawBody,
			SubmittedAt: time.Now(),
		})
	}

	// Write durable status for REST query lifecycle tracking.
	if r.store != nil {
		_ = r.store.SetMessageStatus(&MessageStatus{
			GwMsgID:   gwMsgID,
			To:        body.To,
			From:      body.From,
			Reference: body.Reference,
			Status:    "accepted",
			UpdatedAt: time.Now(),
		})
	}

	// Store callback record if callback URL provided
	callbackURL := body.CallbackURL
	if callbackURL != "" && r.store != nil {
		_ = r.store.SetJSON("callback:"+gwMsgID, &CallbackRecord{
			GwMsgID:     gwMsgID,
			CallbackURL: callbackURL,
			Reference:   body.Reference,
			Retries:     0,
		})
	}

	// Enqueue for southbound forwarding
	select {
	case r.forwardCh <- forwardTask{conn: nil, gwMsgID: gwMsgID, destAddr: body.To, sourceAddr: body.From, rawBody: rawBody}:
	default:
		r.enqueueSubmitRetryOrFail(gwMsgID, "rest-api", body.To, body.From, rawBody, 0)
	}

	r.metrics.SubmitTotal.WithLabelValues("rest_accepted").Inc()

	writeJSON(w, http.StatusAccepted, SMSSubmitResponse{
		ID:        gwMsgID,
		Status:    "accepted",
		To:        body.To,
		Reference: body.Reference,
	})
}

// HandleHTTPBatchSubmit handles POST /api/v1/sms/batch
func (r *Router) HandleHTTPBatchSubmit(w http.ResponseWriter, req *http.Request) {
	var batch SMSBatchRequest
	if err := json.NewDecoder(req.Body).Decode(&batch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(batch.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "messages array is empty"})
		return
	}
	if len(batch.Messages) > 1000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max 1000 messages per batch"})
		return
	}

	results := make([]SMSSubmitResponse, 0, len(batch.Messages))
	for i, msg := range batch.Messages {
		if msg.CallbackURL == "" {
			msg.CallbackURL = batch.CallbackURL
		}
		if msg.Reference == "" && batch.ReferencePrefix != "" {
			msg.Reference = fmt.Sprintf("%s-%d", batch.ReferencePrefix, i)
		}

		gwMsgID := r.nextMsgID()
		rawBody := BuildSubmitSMBody(msg.From, msg.To, []byte(msg.Body))

		corr := &correlation{
			GwMsgID: gwMsgID, NorthConnID: "rest-api",
			MSISDN: msg.To, SubmittedAt: time.Now(),
		}
		r.gwCorrelation.Set(gwMsgID, corr)

		if r.store != nil {
			_ = r.store.StoreSubmit(&SubmitRecord{
				GwMsgID: gwMsgID, NorthConnID: "rest-api",
				MSISDN: msg.To, SourceAddr: msg.From,
				Payload: rawBody, SubmittedAt: time.Now(),
			})
		}

		// Write durable status for REST query lifecycle tracking.
		if r.store != nil {
			_ = r.store.SetMessageStatus(&MessageStatus{
				GwMsgID:   gwMsgID,
				To:        msg.To,
				From:      msg.From,
				Reference: msg.Reference,
				Status:    "accepted",
				UpdatedAt: time.Now(),
			})
		}

		if msg.CallbackURL != "" && r.store != nil {
			_ = r.store.SetJSON("callback:"+gwMsgID, &CallbackRecord{
				GwMsgID: gwMsgID, CallbackURL: msg.CallbackURL,
				Reference: msg.Reference,
			})
		}

		select {
		case r.forwardCh <- forwardTask{conn: nil, gwMsgID: gwMsgID, destAddr: msg.To, sourceAddr: msg.From, rawBody: rawBody}:
		default:
			r.enqueueSubmitRetryOrFail(gwMsgID, "rest-api", msg.To, msg.From, rawBody, 0)
		}

		results = append(results, SMSSubmitResponse{ID: gwMsgID, Status: "accepted", To: msg.To, Reference: msg.Reference})
	}

	r.metrics.SubmitTotal.WithLabelValues("rest_accepted").Add(float64(len(results)))
	writeJSON(w, http.StatusAccepted, results)
}

// HandleHTTPQuery handles GET /api/v1/sms/{id}
func (r *Router) HandleHTTPQuery(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id") // Go 1.22+ pattern matching
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	// Check durable status record first — tracks full lifecycle.
	if r.store != nil {
		st, err := r.store.GetMessageStatus(id)
		if err == nil && st != nil {
			resp := map[string]any{
				"id":     id,
				"status": st.Status,
				"to":     st.To,
			}
			if st.From != "" {
				resp["from"] = st.From
			}
			if st.Reference != "" {
				resp["reference"] = st.Reference
			}
			if st.DLRStatus != "" {
				resp["dlr_status"] = st.DLRStatus
			}
			if st.SmscMsgID != "" {
				resp["smsc_msg_id"] = st.SmscMsgID
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}

	// Fallback: check Pebble gw:{id} for pre-forward state.
	if r.store != nil {
		record, err := r.store.GetSubmitByGwID(id)
		if err == nil && record != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": id, "status": "pending", "to": record.MSISDN,
			})
			return
		}
	}

	// Fallback: check in-memory correlation.
	if corr, ok := r.gwCorrelation.Get(id); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "status": "submitted", "to": corr.MSISDN,
		})
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]string{"error": "message not found"})
}

// RegisterRESTRoutes mounts the REST API endpoints.
func (r *Router) RegisterRESTRoutes(mux *http.ServeMux, keyStore *APIKeyStore) {
	auth := APIKeyAuthMiddleware(keyStore)
	mux.Handle("POST /api/v1/sms", auth(http.HandlerFunc(r.HandleHTTPSubmit)))
	mux.Handle("POST /api/v1/sms/batch", auth(http.HandlerFunc(r.HandleHTTPBatchSubmit)))
	mux.Handle("GET /api/v1/sms/{id}", auth(http.HandlerFunc(r.HandleHTTPQuery)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
