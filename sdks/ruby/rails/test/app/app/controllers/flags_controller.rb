# frozen_string_literal: true

# FlagsController serves what the test suite asserts on: a flag's detail
# for the request's context, an exposure, and the context itself, also as a view sees it.
class FlagsController < ApplicationController
  def show
    detail = Flagnetic.client.detail(params[:key], default: false, context: flagnetic_context)
    render json: { pid: Process.pid, value: detail.value, reason: detail.reason, stage: detail.stage }
  end

  def exposure
    Flagnetic.client.string(params[:key], default: "", context: flagnetic_context)
    Flagnetic.client.exposure(params[:key], context: flagnetic_context)
    head :no_content
  end

  def context
    render json: flagnetic_context
  end

  def context_view
    render inline: "<%= flagnetic_anonymous_id %> <%== flagnetic_context[:units].to_json %>"
  end
end
