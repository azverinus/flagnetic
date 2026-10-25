# frozen_string_literal: true

# SessionsController signs a user in the way most applications do: a new
# session holding the user ID.
class SessionsController < ApplicationController
  def create
    reset_session
    session[:user_id] = params[:user_id]
    head :no_content
  end
end
