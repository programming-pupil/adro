#!/usr/bin/env ruby
# frozen_string_literal: true

require 'yaml'

ROOT = File.expand_path('..', __dir__)
PATH = File.join(ROOT, 'openapi', 'openapi.yaml')
METHODS = %w[get post put patch delete head].freeze
MUTATIONS = %w[post put patch].freeze

def ref(path)
  { '$ref' => path }
end

def api_operation?(path)
  path.start_with?('/api/')
end

def exempt_idempotency?(path)
  path == '/api/v1/auth/login' || path == '/api/v1/auth/logout'
end

def problem_response(description = 'Problem Details response')
  {
    'description' => description,
    'headers' => { 'X-Request-ID' => ref('#/components/headers/RequestID') },
    'content' => { 'application/problem+json' => { 'schema' => ref('#/components/schemas/Problem') } }
  }
end

def ensure_components(document)
  components = document['components'] ||= {}
  components['headers'] ||= {}
  components['headers']['RequestID'] = {
    'description' => 'Stable correlation identifier for this HTTP request.',
    'schema' => { 'type' => 'string' }
  }
  components['headers']['IdempotencyReplayed'] = {
    'description' => 'True when the status, headers, and body were replayed from the first completed request.',
    'schema' => { 'type' => 'string', 'const' => 'true' }
  }
  components['parameters'] ||= {}
  components['parameters']['IdempotencyKey'] = {
    'name' => 'Idempotency-Key',
    'in' => 'header',
    'required' => false,
    'description' => 'Optional for POST, PUT, and PATCH. Scope is tenant + method + path. Reuse with the same query, content type, and body replays the first completed response; reuse with different input returns 409 idempotency_key_conflict.',
    'schema' => { 'type' => 'string', 'minLength' => 1, 'maxLength' => 255 }
  }
  components['schemas'] ||= {}
  components['schemas']['JsonObject'] = {
    'type' => 'object',
    'description' => 'Versioned JSON resource or page. Resource-specific fields remain backward-compatible within /api/v1.',
    'additionalProperties' => true
  }
  components['schemas']['Problem'] = {
    'type' => 'object',
    'required' => %w[type title status detail error_code request_id],
    'properties' => {
      'type' => { 'type' => 'string', 'format' => 'uri' },
      'title' => { 'type' => 'string' },
      'status' => { 'type' => 'integer', 'minimum' => 400, 'maximum' => 599 },
      'detail' => { 'type' => 'string' },
      'error_code' => { 'type' => 'string' },
      'request_id' => { 'type' => 'string' }
    },
    'additionalProperties' => true
  }
  components['schemas']['RunSnapshot'] = {
    'type' => 'object',
    'required' => %w[id status usage],
    'properties' => {
      'id' => { 'type' => 'string' },
      'work_item_id' => { 'type' => 'string' },
      'provider_issue_id' => { 'type' => 'string' },
      'status' => { 'type' => 'string' },
      'last_event_id' => { 'type' => 'string' },
      'session_id' => { 'type' => 'string' },
      'work_dir' => { 'type' => 'string' },
      'baseline_commit' => { 'type' => 'string' },
      'head_commit' => { 'type' => 'string' },
      'submission_url' => { 'type' => 'string', 'format' => 'uri' },
      'checks_conclusion' => { 'type' => 'string' },
      'started_at' => { 'type' => 'string', 'format' => 'date-time' },
      'finished_at' => { 'type' => 'string', 'format' => 'date-time' },
      'usage' => ref('#/components/schemas/Usage')
    }
  }
  components['schemas']['Usage'] = {
    'type' => 'object',
    'required' => %w[input_tokens output_tokens cache_read_tokens cache_write_tokens duration_ms estimated_cost],
    'properties' => {
      'input_tokens' => { 'type' => 'integer', 'format' => 'int64', 'minimum' => 0 },
      'output_tokens' => { 'type' => 'integer', 'format' => 'int64', 'minimum' => 0 },
      'cache_read_tokens' => { 'type' => 'integer', 'format' => 'int64', 'minimum' => 0 },
      'cache_write_tokens' => { 'type' => 'integer', 'format' => 'int64', 'minimum' => 0 },
      'duration_ms' => { 'type' => 'integer', 'format' => 'int64', 'minimum' => 0 },
      'estimated_cost' => { 'type' => 'number', 'minimum' => 0 }
    }
  }
end

def response_schema(path, method, code)
  return nil if code == '204' || method == 'head'
  return ['text/plain', { 'type' => 'string' }] if path == '/metrics'
  if path == '/api/v1/artifacts/{id}/versions/{version}/content'
    return ['application/octet-stream', { 'type' => 'string', 'format' => 'binary' }]
  end
  return ['application/problem+json', ref('#/components/schemas/Problem')] if code.match?(/\A[45]/)
  return ['application/json', ref('#/components/schemas/RunSnapshot')] if path == '/api/v1/runs/{id}' && code == '200'
  return ['application/json', ref('#/components/schemas/Usage')] if path == '/api/v1/runs/{id}/usage' && code == '200'
  ['application/json', ref('#/components/schemas/JsonObject')]
end

def normalize(document)
  ensure_components(document)
  document.fetch('paths').each do |path, item|
    item.each do |method, operation|
      next unless METHODS.include?(method)
      if MUTATIONS.include?(method) && api_operation?(path) && !exempt_idempotency?(path)
        operation['parameters'] ||= []
        unless operation['parameters'].any? { |parameter| parameter['$ref'] == '#/components/parameters/IdempotencyKey' }
          operation['parameters'] << ref('#/components/parameters/IdempotencyKey')
        end
      end
      responses = operation['responses'] ||= {}
      responses.each do |code, response|
        schema = response_schema(path, method, code.to_s)
        next unless schema
        media_type, definition = schema
        response['content'] ||= {}
        response['content'][media_type] ||= { 'schema' => definition }
        response['headers'] ||= {}
        response['headers']['X-Request-ID'] ||= ref('#/components/headers/RequestID')
        if MUTATIONS.include?(method) && code.to_s.match?(/\A2/) && !exempt_idempotency?(path)
          response['headers']['Idempotency-Replayed'] ||= ref('#/components/headers/IdempotencyReplayed')
        end
      end
      responses['default'] ||= problem_response
    end
  end
  document
end

def validate(document)
  errors = []
  document.fetch('paths').each do |path, item|
    item.each do |method, operation|
      next unless METHODS.include?(method)
      if MUTATIONS.include?(method) && api_operation?(path) && !exempt_idempotency?(path)
        refs = (operation['parameters'] || []).map { |parameter| parameter['$ref'] }
        errors << "#{method.upcase} #{path} lacks Idempotency-Key" unless refs.include?('#/components/parameters/IdempotencyKey')
      end
      errors << "#{method.upcase} #{path} lacks default Problem response" unless operation.fetch('responses', {}).key?('default')
      operation.fetch('responses', {}).each do |code, response|
        next if code.to_s == '204' || code.to_s == 'default' || method == 'head'
        schemas = (response['content'] || {}).values.map { |entry| entry['schema'] }.compact
        errors << "#{method.upcase} #{path} response #{code} lacks a schema" if schemas.empty?
      end
    end
  end
  errors
end

document = YAML.load_file(PATH)
if ARGV.first == '--fix'
  normalized = normalize(document)
  File.write(PATH, YAML.dump(normalized))
  document = YAML.load_file(PATH)
end
errors = validate(document)
unless errors.empty?
  warn errors.join("\n")
  exit 1
end
puts "OpenAPI response, Problem Details, and idempotency contracts are complete"
