#!/usr/bin/env ruby
# frozen_string_literal: true

# Generate the release coverage inventory from the sources that define the
# public surface. The ledger deliberately distinguishes inventory coverage
# from behavioral test evidence; a generated row is not a claim that an E2E
# case passed.

require 'fileutils'
require 'json'
require 'open3'
require 'time'
require 'yaml'

ROOT = File.expand_path('..', __dir__)
METHODS = %w[get post put patch delete head options].freeze
EXPECTED_MENU_COUNT = 19

class CoverageLedger
  attr_reader :root, :output_dir

  def initialize(root, output_dir)
    @root = root
    @output_dir = output_dir
  end

  def generate
    source_sha = git_sha
    openapi = read_openapi
    menus = read_menus
    actions = read_dom_actions
    operations = read_operations(openapi, menus)
    rows = operations + menus.map.with_index { |menu, index| menu_row(menu, index, source_sha) } + actions.map.with_index { |action, index| action_row(action, index, source_sha) }
    gaps = []
    gaps.concat(operations.select { |item| item['operation_id_source'] == 'derived' }.map { |item| "#{item['method']} #{item['path']} has no OpenAPI operationId" })
    errors = validate(source_sha, operations, menus, actions, rows)
    report = {
      'schema_version' => 1,
      'generated_at' => Time.now.utc.iso8601,
      'source_sha' => source_sha,
      'source_dirty' => dirty?,
      'counts' => {
        'openapi_operations' => operations.length,
        'menus' => menus.length,
        'dom_actions' => actions.length,
        'ledger_rows' => rows.length
      },
      'baseline_note' => 'The inventory is derived from the current checkout. Historical counts such as 152 are not used as a gate.',
      'contract_gaps' => gaps,
      'errors' => errors,
      'operations' => operations,
      'menus' => menus.map.with_index { |menu, index| menu_entry(menu, index, source_sha) },
      'dom_actions' => actions.map.with_index { |action, index| action_entry(action, index, source_sha) },
      'rows' => rows
    }
    write_report(report, operations, menus, actions, rows)
    report
  end

  private

  def read_openapi
    YAML.load_file(File.join(root, 'openapi', 'openapi.yaml'))
  end

  def read_operations(document, menus)
    paths = document.fetch('paths', {})
    items = []
    paths.each do |path, path_item|
      path_item.each do |method, operation|
        next unless METHODS.include?(method)

        source = operation.is_a?(Hash) && operation['operationId'] && !operation['operationId'].to_s.empty? ? 'declared' : 'derived'
        operation_id = source == 'declared' ? operation['operationId'].to_s : derived_operation_id(path, method)
        items << {
          'operation_id' => operation_id,
          'operation_id_source' => source,
          'method' => method.upcase,
          'path' => path,
          'menu_id' => menu_for_path(path, menus),
          'summary' => operation.is_a?(Hash) ? operation['summary'].to_s : '',
          'case_id' => format('API-CONTRACT-%03d', items.length + 1),
          'test_file' => 'scripts/coverage-ledger.rb',
          'test_function' => 'validate_openapi_inventory',
          'layer' => 'L0-contract-inventory',
          'fixture' => 'openapi/openapi.yaml',
          'expected_state' => 'operation is present exactly once in the generated inventory',
          'last_sha' => git_sha,
          'evidence' => evidence_path('openapi_operations.json'),
          'verification_status' => 'inventory-only'
        }
      end
    end
    items
  end

  def read_menus
    source = File.read(File.join(root, 'apps', 'web', 'enhancements.js'))
    block = source[/const menuIDs\s*=\s*\[(.*?)\]/m, 1]
    raise 'apps/web/enhancements.js does not define const menuIDs' unless block

    block.scan(/['"]([^'"]+)['"]/).flatten.uniq
  end

  def read_dom_actions
    files = %w[apps/web/index.html apps/web/enhancements.js]
    actions = []
    files.each do |relative|
      path = File.join(root, relative)
      source = File.read(path)
      source.scan(/<button\b([^>]*)>/im).each_with_index do |match, index|
        attributes = match[0]
        data_attributes = attributes.scan(/\b(data-[a-z0-9_-]+)(?:\s*=\s*(["'])(.*?)\2)?/im).map do |name, _quote, value|
          [name.downcase, value.to_s]
        end
        id = attributes[/\bid\s*=\s*(["'])(.*?)\1/im, 2]
        type = attributes[/\btype\s*=\s*(["'])(.*?)\1/im, 2]
        next if data_attributes.empty? && id.to_s.empty? && type.to_s != 'submit'

        fragment = "<button#{attributes}>"
        offset = source.index(fragment)
        line = offset ? source[0...offset].count("\n") + 1 : index + 1
        actions << {
          'source_file' => relative,
          'source_line' => line,
          'element' => 'button',
          'id' => id.to_s,
          'type' => type.to_s,
          'data_attributes' => data_attributes.to_h,
          'menu_id' => data_attributes.to_h['data-view'].to_s.empty? ? 'global' : data_attributes.to_h['data-view'],
          'selector' => id.to_s.empty? ? "button[data-source-line=\"#{line}\"]" : "##{id}"
        }
      end
    end
    actions.uniq { |item| [item['source_file'], item['source_line'], item['selector'], item['data_attributes']] }
  end

  def menu_entry(menu, index, source_sha)
    {
      'menu_id' => menu,
      'case_id' => format('MENU-CONTROL-%02d', index + 1),
      'test_file' => 'scripts/coverage-ledger.rb',
      'test_function' => 'validate_menu_inventory',
      'layer' => 'L0-ui-inventory',
      'fixture' => 'apps/web/enhancements.js',
      'expected_state' => 'menu is declared once and has a stable menu_id',
      'last_sha' => source_sha,
      'evidence' => evidence_path('menus.json'),
      'verification_status' => 'inventory-only'
    }
  end

  def menu_row(menu, index, source_sha)
    menu_entry(menu, index, source_sha).merge('operation_id' => nil, 'action_id' => "menu:#{menu}")
  end

  def action_entry(action, index, source_sha)
    {
      'action_id' => format('BUTTON-ACTION-%03d', index + 1),
      'menu_id' => action['menu_id'],
      'selector' => action['selector'],
      'source_file' => action['source_file'],
      'source_line' => action['source_line'],
      'element' => action['element'],
      'id' => action['id'],
      'type' => action['type'],
      'data_attributes' => action['data_attributes'],
      'case_id' => format('BUTTON-ACTION-%03d', index + 1),
      'test_file' => 'scripts/coverage-ledger.rb',
      'test_function' => 'validate_dom_action_inventory',
      'layer' => 'L0-ui-inventory',
      'fixture' => action['source_file'],
      'expected_state' => 'actionable button is represented by a stable selector and source location',
      'last_sha' => source_sha,
      'evidence' => evidence_path('dom_actions.json'),
      'verification_status' => 'inventory-only'
    }
  end

  def action_row(action, index, source_sha)
    action_entry(action, index, source_sha).merge('operation_id' => nil)
  end

  def menu_for_path(path, menus)
    return 'admin' if path.start_with?('/api/v1/auth/', '/api/v1/users', '/api/v1/directory', '/api/v1/audit')
    return 'requirements' if path.include?('/requirements') || path.include?('/work-items') || path.include?('/impact-reports')
    return 'bugs' if path.include?('/bugs')
    return 'chats' if path.include?('/chats') || path.include?('/sessions')
    return 'agents' if path.include?('/agents') || path.include?('/developer-profiles')
    return 'mcp' if path.include?('/mcp')
    return 'skills' if path.include?('/skills')
    return 'automations' if path.include?('/automations')
    return 'repositories' if path.include?('/repositories') || path.include?('/repository-graph') || path.include?('/team-workspaces')
    return 'artifacts' if path.include?('/artifacts') || path.include?('/attachments') || path.include?('/screenshots') || path.include?('/artifact-migrations')
    return 'runners' if path.include?('/runners')
    return 'executions' if path.include?('/execution-plans') || path.include?('/pipelines') || path.include?('/runs') || path.include?('/approvals')
    return 'testing' if path.include?('/evidence')
    return 'integrations' if path.include?('/provider/') || path.include?('/system/') || path == '/healthz' || path == '/readyz'
    return 'cost' if path == '/metrics' || path.include?('/usage')
    menus.include?('workbench') ? 'workbench' : 'global'
  end

  def derived_operation_id(path, method)
    slug = path.gsub(/\{([^}]+)\}/, 'by_\\1').gsub(%r{[^a-zA-Z0-9]+}, '_').sub(/\A_/, '').sub(/_\z/, '').downcase
    "#{method.downcase}_#{slug}"
  end

  def validate(source_sha, operations, menus, actions, rows)
    errors = []
    errors << "expected #{EXPECTED_MENU_COUNT} menus, found #{menus.length}" unless menus.length == EXPECTED_MENU_COUNT
    errors << 'menu ids must be unique' unless menus.uniq.length == menus.length
    operation_keys = operations.map { |item| [item['method'], item['path']] }
    errors << 'OpenAPI method/path pairs must be unique' unless operation_keys.uniq.length == operation_keys.length
    operation_ids = operations.map { |item| item['operation_id'] }
    errors << 'operation ids must be unique after derivation' unless operation_ids.uniq.length == operation_ids.length
    action_ids = rows.map { |item| item['action_id'] }.compact
    errors << 'action ids must be unique' unless action_ids.uniq.length == action_ids.length
    errors << 'ledger rows must retain the current source SHA' unless rows.all? { |item| item['last_sha'] == source_sha }
    errors
  end

  def write_report(report, operations, menus, actions, rows)
    FileUtils.mkdir_p(output_dir)
    File.write(File.join(output_dir, 'openapi_operations.json'), JSON.pretty_generate(operations) + "\n")
    File.write(File.join(output_dir, 'menus.json'), JSON.pretty_generate(menus.map.with_index { |menu, index| menu_entry(menu, index, report['source_sha']) }) + "\n")
    File.write(File.join(output_dir, 'dom_actions.json'), JSON.pretty_generate(actions.map.with_index { |action, index| action_entry(action, index, report['source_sha']) }) + "\n")
    File.write(File.join(output_dir, 'ledger.json'), JSON.pretty_generate(rows) + "\n")
    File.write(File.join(output_dir, 'summary.json'), JSON.pretty_generate(report.reject { |key, _| %w[operations menus dom_actions rows].include?(key) }) + "\n")
    File.write(File.join(output_dir, 'report.json'), JSON.pretty_generate(report) + "\n")
  end

  def evidence_path(filename)
    "var/test-report/coverage-ledger/#{git_sha}/#{filename}"
  end

  def git_sha
    stdout, status = Open3.capture2('git', '-C', root, 'rev-parse', 'HEAD')
    status.success? ? stdout.strip : 'unknown'
  end

  def dirty?
    _stdout, status = Open3.capture2('git', '-C', root, 'diff', '--quiet')
    !status.success?
  end
end

def option(name, default)
  index = ARGV.index(name)
  return default unless index

  ARGV[index + 1] || default
end

sha_stdout, sha_status = Open3.capture2('git', '-C', ROOT, 'rev-parse', 'HEAD')
sha = sha_status.success? ? sha_stdout.strip : 'unknown'
output = File.expand_path(option('--output', File.join(ROOT, 'var', 'test-report', 'coverage-ledger', sha)))
ledger = CoverageLedger.new(ROOT, output).generate

puts JSON.generate('status' => ledger['errors'].empty? ? 'passed' : 'failed', 'source_sha' => ledger['source_sha'], 'counts' => ledger['counts'], 'contract_gaps' => ledger['contract_gaps'].length, 'errors' => ledger['errors'])
exit(1) unless ledger['errors'].empty?
