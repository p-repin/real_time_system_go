package http

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"real_time_system/domain/entity"
	"real_time_system/internal/service"
	"real_time_system/internal/service/dto"
)

type CartHandler struct {
	cartService *service.CartService
}

func NewCartHandler(cartService *service.CartService) *CartHandler {
	return &CartHandler{cartService: cartService}
}

func (h *CartHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/cart", func(r chi.Router) {
		r.Post("/items", h.AddToCart)
		r.Get("/", h.GetCart)
		r.Patch("/items", h.UpdateQuantity)
		r.Delete("/items/{product_id}", h.RemoveFromCart)
		r.Delete("/", h.ClearCart)
	})
}

// @Summary      Add item to cart
// @Description  Добавляет товар в корзину пользователя
// @Tags         Cart
// @Accept       json
// @Produce      json
// @Param        X-User-ID  header    string                true  "User ID (UUID)"
// @Param        request    body      dto.AddToCartRequest  true  "Item to add"
// @Success      200        {object}  dto.CartResponse
// @Failure      400        {object}  ErrorResponse
// @Router       /api/v1/cart/items [post]
func (h *CartHandler) AddToCart(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	var req dto.AddToCartRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cart, err := h.cartService.AddToCart(r.Context(), userID, req)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, cart)
}

// @Summary      Get cart with items
// @Description  Получает корзину с товарами
// @Tags         Cart
// @Accept       json
// @Produce      json
// @Param        X-User-ID  header    string                true  "User ID (UUID)"
// @Success      200        {object}  dto.CartResponse
// @Failure      400        {object}  ErrorResponse
// @Router       /api/v1/cart [get]
func (h *CartHandler) GetCart(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	cart, err := h.cartService.GetCartWithItems(r.Context(), userID)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, cart)
}

// @Summary      Update quantity to cart
// @Description  Обновляет количество товара в корзине
// @Tags         Cart
// @Accept       json
// @Produce      json
// @Param        X-User-ID  header    string                true  "User ID (UUID)"
// @Param        request    body      dto.UpdateCartItemRequest  true  "Update to cart"
// @Success      200        {object}  dto.CartResponse
// @Failure      400        {object}  ErrorResponse
// @Router       /api/v1/cart/items [patch]
func (h *CartHandler) UpdateQuantity(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	var req dto.UpdateCartItemRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cart, err := h.cartService.UpdateQuantity(r.Context(), userID, req)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, cart)
}

// @Summary      Remove product from cart
// @Description  Удаляет продукт из корзины
// @Tags         Cart
// @Accept       json
// @Produce      json
// @Param        X-User-ID  header    string                true  "User ID (UUID)"
// @Param 		product_id path string true "Product ID (UUID)"
// @Success      200        {object}  dto.CartResponse
// @Failure      400        {object}  ErrorResponse
// @Router       /api/v1/cart/items/{product_id} [delete]
func (h *CartHandler) RemoveFromCart(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}
	idParam := chi.URLParam(r, "product_id")

	cart, err := h.cartService.RemoveFromCart(r.Context(), userID, idParam)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, cart)

}

// @Summary      Clear cart
// @Description  Очищает корзину
// @Tags         Cart
// @Accept       json
// @Produce      json
// @Param        X-User-ID  header    string                true  "User ID (UUID)"
// @Success      200        {object}  dto.CartResponse
// @Failure      400        {object}  ErrorResponse
// @Router       /api/v1/cart [delete]
func (h *CartHandler) ClearCart(w http.ResponseWriter, r *http.Request) {
	userID, ok := headerToUserID(w, r)
	if !ok {
		return
	}

	cart, err := h.cartService.ClearCart(r.Context(), userID)
	if err != nil {
		HandleError(w, err)
		return
	}

	JSON(w, http.StatusOK, cart)
}

func headerToUserID(w http.ResponseWriter, r *http.Request) (entity.UserID, bool) {
	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		Error(w, http.StatusUnauthorized, "missing X-User-ID header")
		return entity.UserID{}, false
	}

	userID, err := entity.ParseUserID(userIDStr)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid user ID format")
		return entity.UserID{}, false
	}
	return userID, true
}
