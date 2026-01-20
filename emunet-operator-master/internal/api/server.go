package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	emunetv1 "github.com/emunet/emunet-operator/api/v1"
	"github.com/emunet/emunet-operator/internal/redis"
)

type Server struct {
	client client.Client
	redis  *redis.Client
	router *mux.Router
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func NewServer(client client.Client, redisClient *redis.Client) *Server {
	s := &Server{
		client: client,
		redis:  redisClient,
		router: mux.NewRouter(),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()

	api.HandleFunc("/emunets", s.listEmuNets).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}", s.getEmuNet).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/status", s.getEmuNetStatus).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/pods", s.listPods).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/pods/{podName}", s.getPod).Methods("GET")
	api.HandleFunc("/health", s.healthCheck).Methods("GET")
}

func (s *Server) GetRouter() *mux.Router {
	return s.router
}

func (s *Server) listEmuNets(w http.ResponseWriter, r *http.Request) {
	emunetList := &emunetv1.EmuNetList{}
	if err := s.client.List(r.Context(), emunetList); err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sendSuccess(w, emunetList.Items)
}

func (s *Server) getEmuNet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	emunet := &emunetv1.EmuNet{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: namespace}, emunet); err != nil {
		s.sendError(w, http.StatusNotFound, err.Error())
		return
	}

	s.sendSuccess(w, emunet)
}

func (s *Server) getEmuNetStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	redisStatus, err := s.redis.GetEmuNetStatus(r.Context(), namespace, name)
	if err == nil && redisStatus != nil {
		s.sendSuccess(w, redisStatus)
		return
	}

	emunet := &emunetv1.EmuNet{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: namespace}, emunet); err != nil {
		s.sendError(w, http.StatusNotFound, err.Error())
		return
	}

	s.sendSuccess(w, emunet.Status)
}

func (s *Server) listPods(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	pods, err := s.redis.ListPodStatuses(r.Context(), namespace, name)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sendSuccess(w, pods)
}

func (s *Server) getPod(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	podName := vars["podName"]

	pod, err := s.redis.GetPodStatus(r.Context(), namespace, name, podName)
	if err != nil {
		s.sendError(w, http.StatusNotFound, err.Error())
		return
	}

	s.sendSuccess(w, pod)
}

func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, map[string]string{"status": "healthy"})
}

func (s *Server) sendSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(Response{
		Success: true,
		Data:    data,
	})
}

func (s *Server) sendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(Response{
		Success: false,
		Error:   message,
	})
}
