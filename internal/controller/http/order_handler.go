package http

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"real_time_system/domain/entity"
	"real_time_system/internal/service"
	"real_time_system/internal/service/dto"
)

type OrderHandler struct {
	orderService *service.OrderService
}

func NewOrderHandler(orderService *service.OrderService) *OrderHandler {
	return &OrderHandler{orderService: orderService}
}

func (h *OrderHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/orders", func(r chi.Router) {
		r.Post("/", h.PlaceOrder)
		r.Get("/", h.GetUserOrders)
		r.Get("/{id}", h.GetOrder)
		r.Patch("/{id}/status", h.UpdateStatus)
	})
}

// PlaceOrder создаёт заказ из корзины пользователя.
//
// @Summary      Place order from cart
// @Description  Создаёт заказ из товаров в корзине. Уменьшает stock, очищает корзину.
// @Tags         Orders
// @Produce      json
// @Param        X-User-ID  header    string             true  "User ID (UUID)"
// @Success      201        {object}  dto.OrderResponse  "Order created"
// @Failure      400        {object}  ErrorResponse      "Invalid request or empty cart"
// @Failure      404        {object}  ErrorResponse      "User or product not found"
// @Failure      409        {object}  ErrorResponse      "Insufficient stock"
// @Failure      500        {object}  ErrorResponse      "Internal server error"
// @Router       /api/v1/orders [post]
func (h *OrderHandler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	order, err := h.orderService.PlaceOrder(r.Context(), userID)
	if err != nil {
		HandleError(w, err)
		return
	}

	Created(w, order)
}

// GetUserOrders возвращает список заказов пользователя.
//
// @Summary      Get user orders
// @Description  Возвращает все заказы текущего пользователя.
// @Tags         Orders
// @Produce      json
// @Param        X-User-ID  header    string               true  "User ID (UUID)"
// @Success      200        {array}   dto.OrderResponse    "Orders list"
// @Failure      400        {object}  ErrorResponse        "Invalid user ID"
// @Failure      500        {object}  ErrorResponse        "Internal server error"
// @Router       /api/v1/orders [get]
func (h *OrderHandler) GetUserOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	orders, err := h.orderService.GetUserOrders(r.Context(), userID)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, orders)

}

// GetOrder возвращает заказ по ID.
//
// @Summary      Get order by ID
// @Description  Возвращает заказ с товарами. Проверяет ownership (только свои заказы).
// @Tags         Orders
// @Produce      json
// @Param        X-User-ID  header    string             true  "User ID (UUID)"
// @Param        id         path      string             true  "Order ID (UUID)"  format(uuid)
// @Success      200        {object}  dto.OrderResponse  "Order found"
// @Failure      400        {object}  ErrorResponse      "Invalid order ID format"
// @Failure      404        {object}  ErrorResponse      "Order not found"
// @Failure      500        {object}  ErrorResponse      "Internal server error"
// @Router       /api/v1/orders/{id} [get]
func (h *OrderHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	orderID, err := entity.ParseOrderID(idParam)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid order id format")
		return
	}

	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	order, err := h.orderService.GetOrder(r.Context(), userID, orderID)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, order)
}

// UpdateStatus обновляет статус заказа.
//
// @Summary      Update order status
// @Description  Обновляет статус заказа. Проверяет допустимость перехода (state machine).
// @Tags         Orders
// @Accept       json
// @Produce      json
// @Param        X-User-ID  header    string                   true  "User ID (UUID)"
// @Param        id         path      string                   true  "Order ID (UUID)"  format(uuid)
// @Param        request    body      dto.UpdateStatusRequest  true  "New status"
// @Success      200        {object}  dto.OrderResponse        "Status updated"
// @Failure      400        {object}  ErrorResponse            "Invalid request or transition"
// @Failure      404        {object}  ErrorResponse            "Order not found"
// @Failure      500        {object}  ErrorResponse            "Internal server error"
// @Router       /api/v1/orders/{id}/status [patch]
func (h *OrderHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	orderID, err := entity.ParseOrderID(idParam)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid order id format")
		return
	}

	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	var req dto.UpdateStatusRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	newStatus, err := h.orderService.UpdateStatus(r.Context(), userID, orderID, req.Status)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, newStatus)
}
