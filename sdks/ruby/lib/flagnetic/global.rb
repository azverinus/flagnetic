# frozen_string_literal: true

# Flagnetic.client: the process-wide client.
module Flagnetic
  # NotConfigured is raised by Flagnetic.client when no client was assigned
  # and no builder was set.
  class NotConfigured < Error; end

  @global_mutex = Mutex.new
  @client = nil
  @client_builder = nil

  class << self
    # client returns the process-wide client: the one assigned with
    # client=, or the one client_builder builds on first use, once, however
    # many threads ask at the same time. The Rails integration sets either.
    # Raises NotConfigured when neither was set.
    #
    #   Flagnetic.client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
    #   Flagnetic.client.bool("new_checkout", default: false, context: { units: { user: "u-1001" } })
    def client
      @client || @global_mutex.synchronize do
        @client ||= @client_builder&.call or
          raise NotConfigured, "Flagnetic.client is not set: assign one with Flagnetic.client= " \
                               "or use the Rails integration"
      end
    end

    # client= assigns the process-wide client. It does not close the one it
    # replaces.
    def client=(client)
      @global_mutex.synchronize { @client = client }
    end

    # client_builder= sets a callable building the process-wide client on
    # the first call to client, unless one is assigned before.
    def client_builder=(builder)
      @global_mutex.synchronize { @client_builder = builder }
    end

    # client? tells whether the process-wide client exists, without
    # building it.
    def client? = !@client.nil?

    private

    # replace_client swaps the assigned client, and the builder unless it
    # is :keep, returning the previous pair; Testing.with_flags restores it
    # without building a client that was never built.
    def replace_client(client, builder = :keep)
      @global_mutex.synchronize do
        previous = [@client, @client_builder]
        @client = client
        @client_builder = builder unless builder == :keep
        previous
      end
    end
  end
end
