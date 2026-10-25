# frozen_string_literal: true

# ApplicationController is the test application's base controller.
class ApplicationController < ActionController::Base
  skip_forgery_protection
end
