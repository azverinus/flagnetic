# frozen_string_literal: true

Rails.application.routes.draw do
  get "/flags/:key", to: "flags#show"
  post "/flags/:key/exposure", to: "flags#exposure"
  get "/context", to: "flags#context"
  get "/context_view", to: "flags#context_view"
  post "/sign_in", to: "sessions#create"
end
