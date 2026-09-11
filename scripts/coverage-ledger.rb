#!/usr/bin/env ruby
# frozen_string_literal: true

# Generate the release coverage inventory from the sources that define the
# public surface. The ledger deliberately distinguishes inventory coverage
# from behavioral test evidence; a generated row is not a claim that an E2E
# case passed.

require 'fileutils'
require 'digest'
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
    rows = []
    operations.each_with_index do |operation, index|
      rows << operation_inventory_row(operation, index, source_sha)
      rows << operation_behavior_row(operation, index, source_sha)
      real_runtime_rows(operation, index, source_sha).each { |row| rows << row }
    end
    menus.each_with_index do |menu, index|
      rows << menu_inventory_row(menu, index, source_sha)
      rows << menu_behavior_row(menu, index, source_sha)
    end
    actions.each_with_index do |action, index|
      rows << action_inventory_row(action, index, source_sha)
      rows << action_behavior_row(action, index, source_sha)
    end
    gaps = []
    gaps.concat(operations.select { |item| item['operation_id_source'] == 'derived' }.map { |item| "#{item['method']} #{item['path']} has no OpenAPI operationId" })
    errors = validate(source_sha, operations, menus, actions, rows)
    report = {
      'schema_version' => 2,
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
      'operations' => operations.map.with_index { |operation, index| operation_entry(operation, index, source_sha) },
      'menus' => menus.map.with_index { |menu, index| menu_entry(menu, index, source_sha) },
      'dom_actions' => actions.map.with_index { |action, index| action_entry(action, index, source_sha) },
      'rows' => rows
    }
    write_report(report, report['operations'], menus, actions, rows)
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
          'handler' => handler_for_path(path)
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
      occurrence_by_fingerprint = Hash.new(0)
      source.to_enum(:scan, /<button\b([^>]*)>(.*?)<\/button>/im).each do
        match = Regexp.last_match
        attributes = match[1]
        label = match[2].to_s.gsub(/<[^>]+>/, ' ').gsub(/\$\{[^}]+\}/, '*').gsub(/\s+/, ' ').strip
        data_attributes = attributes.scan(/\b(data-[a-z0-9_-]+)(?:\s*=\s*(["'])(.*?)\2)?/im).map do |name, _quote, value|
          [name.downcase, value.to_s]
        end
        id = attributes[/\bid\s*=\s*(["'])(.*?)\1/im, 2]
        type = attributes[/\btype\s*=\s*(["'])(.*?)\1/im, 2]
        next if data_attributes.empty? && id.to_s.empty? && type.to_s != 'submit'

        offset = match.begin(0)
        line = source[0...offset].count("\n") + 1
        stable_material = [relative, attributes.gsub(/\$\{[^}]+\}/, '*').gsub(/\s+/, ' ').strip, label].join('|')
        fingerprint = Digest::SHA256.hexdigest(stable_material)[0, 16]
        occurrence_by_fingerprint[fingerprint] += 1
        action_id = "button:#{fingerprint}:#{occurrence_by_fingerprint[fingerprint]}"
        selector = stable_button_selector(id.to_s, type.to_s, data_attributes.to_h)
        actions << {
          'action_id' => action_id,
          'source_file' => relative,
          'source_line' => line,
          'element' => 'button',
          'id' => id.to_s,
          'type' => type.to_s,
          'data_attributes' => data_attributes.to_h,
          'label' => label,
          'menu_id' => data_attributes.to_h['data-view'].to_s.empty? ? 'global' : data_attributes.to_h['data-view'],
          'selector' => selector
        }
      end
    end
    actions
  end

  def stable_button_selector(id, type, data_attributes)
    return "##{id}" unless id.empty?

    preferred = %w[data-view data-orchestration-action data-comment-retry data-comment-reply data-close-orchestration data-graph-remove data-edit-user]
    key = (preferred & data_attributes.keys).first || data_attributes.keys.sort.first
    if key
      value = data_attributes[key].to_s
      return "button[#{key}]" if value.empty? || value.include?('${')

      return "button[#{key}=\"#{value}\"]"
    end
    return 'button[type="submit"]' if type == 'submit'

    'button'
  end

  def menu_entry(menu, index, source_sha)
    {
      'menu_id' => menu,
      'case_id' => format('MENU-CONTROL-%02d', index + 1),
      'test_file' => 'e2e/coverage-ledger.spec.js',
      'test_function' => 'release coverage ledger renders every declared menu',
      'layer' => 'L3-browser',
      'fixture' => 'apps/web/enhancements.js#menuIDs',
      'expected_state' => 'menu renders, navigates, refreshes, and remains authorized by the server',
      'last_sha' => source_sha,
      'evidence' => "var/test-report/browser/#{source_sha}/menus.json",
      'verification_status' => 'executable'
    }
  end

  def menu_inventory_row(menu, index, source_sha)
    menu_entry(menu, index, source_sha).merge(
      'operation_id' => nil, 'action_id' => "menu:#{menu}",
      'test_file' => 'scripts/coverage-ledger.rb', 'test_function' => 'validate',
      'layer' => 'L0-ui-inventory', 'evidence' => evidence_path('menus.json'),
      'expected_state' => 'menu is declared once and has a stable menu_id', 'verification_status' => 'inventory-only'
    )
  end

  def menu_behavior_row(menu, index, source_sha)
    menu_entry(menu, index, source_sha).merge('operation_id' => nil, 'action_id' => "menu:#{menu}")
  end

  def action_entry(action, index, source_sha)
    {
      'action_id' => action['action_id'],
      'menu_id' => action['menu_id'],
      'selector' => action['selector'],
      'source_file' => action['source_file'],
      'source_line' => action['source_line'],
      'element' => action['element'],
      'id' => action['id'],
      'type' => action['type'],
      'label' => action['label'],
      'data_attributes' => action['data_attributes'],
      'case_id' => format('BUTTON-ACTION-%03d', index + 1),
      'test_file' => 'e2e/coverage-ledger.spec.js',
      'test_function' => 'release coverage ledger exposes every registered action',
      'layer' => 'L3-browser',
      'fixture' => action['source_file'],
      'expected_state' => 'action is rendered with stable identity and remains usable after refresh/error recovery',
      'last_sha' => source_sha,
      'evidence' => "var/test-report/browser/#{source_sha}/actions.json",
      'verification_status' => 'executable'
    }
  end

  def action_inventory_row(action, index, source_sha)
    action_entry(action, index, source_sha).merge(
      'operation_id' => nil, 'test_file' => 'scripts/coverage-ledger.rb',
      'test_function' => 'validate', 'layer' => 'L0-ui-inventory',
      'expected_state' => 'actionable button is present in the generated source inventory',
      'evidence' => evidence_path('dom_actions.json'), 'verification_status' => 'inventory-only'
    )
  end

  def action_behavior_row(action, index, source_sha)
    action_entry(action, index, source_sha).merge('operation_id' => nil)
  end

  def operation_entry(operation, index, source_sha)
    operation.merge(
      'case_id' => format('API-CONTRACT-%03d', index + 1),
      'test_file' => 'internal/api/openapi_operation_matrix_test.go',
      'test_function' => "TestOpenAPIOperationMatrix/#{operation['operation_id']}",
      'layer' => 'L2-api-integration',
      'fixture' => "openapi/openapi.yaml##{operation['operation_id']}",
      'expected_state' => 'declared method reaches the mapped handler, returns request correlation, and fails closed for minimal input',
      'last_sha' => source_sha,
      'evidence' => "var/test-report/api-operation-matrix/#{source_sha}/report.json##{operation['operation_id']}",
      'verification_status' => 'executable'
    )
  end

  def operation_inventory_row(operation, index, source_sha)
    operation_entry(operation, index, source_sha).merge(
      'action_id' => nil, 'test_file' => 'scripts/coverage-ledger.rb',
      'test_function' => 'validate', 'layer' => 'L0-contract-inventory',
      'expected_state' => 'operationId, method/path and mapped handler are unique',
      'evidence' => evidence_path('openapi_operations.json'), 'verification_status' => 'inventory-only'
    )
  end

  def operation_behavior_row(operation, index, source_sha)
    operation_entry(operation, index, source_sha).merge('action_id' => nil)
  end

  def real_runtime_rows(operation, index, source_sha)
    suites = []
    path = operation['path']
    suites << 'scripts/real-graph-orchestration-e2e.sh' if path.include?('/execution-plans') || path.include?('/plans/') || path.include?('/workspaces/{workspace_id}/agents') || path.include?('/squads')
    suites << 'scripts/comment-handoff-real-e2e.sh' if path.include?('/comments')
    suites << 'scripts/real-pipeline-e2e.sh' if path.include?('/pipelines') || path.include?('/bugs/')
    suites << 'scripts/release-system-e2e.sh' if path.include?('/sessions') || path.include?('/work-items') || path.include?('/runs/')
    suites.uniq.map.with_index do |suite, suite_index|
      operation_entry(operation, index, source_sha).merge(
        'action_id' => nil,
        'case_id' => format('REAL-OP-%03d-%02d', index + 1, suite_index + 1),
        'test_file' => suite,
        'test_function' => 'main',
        'layer' => 'L4-real-runtime',
        'fixture' => 'local temporary git repository + authenticated local runtime',
        'expected_state' => 'real process evidence binds PID, workdir, context, event cursor and artifact/stdout/stderr hashes',
        'evidence' => "var/test-report/real-runtime/<run-id>/manifest.json##{operation['operation_id']}",
        'verification_status' => 'requires-current-sha-evidence'
      )
    end
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

  def handler_for_path(path)
    return 'login' if path == '/api/v1/auth/login'
    return 'authMe' if path == '/api/v1/auth/me'
    return 'logout' if path == '/api/v1/auth/logout'
    return 'userRoute' if path.start_with?('/api/v1/users')
    return 'directory' if path == '/api/v1/directory'
    return 'providerDiagnostics' if path == '/api/v1/provider/diagnostics'
    return 'systemDiagnostics' if path == '/api/v1/system/diagnostics'
    return 'ready' if path == '/readyz'
    return 'metrics' if path == '/metrics'
    return 'workspaceMigrationRoute' if path.include?('/workspaces/{workspace_id}/migration/')
    return 'orchestrationWorkspaceRoute' if path.include?('/workspaces/{workspace_id}/agents') || path.start_with?('/api/v1/squads')
    return 'orchestrationRoute' if path.start_with?('/api/v1/execution-plans')
    return 'planTimeline' if path.start_with?('/api/v1/plans/')
    return 'runReplay' if path.end_with?('/replay') && path.include?('/runs/')
    return 'runDiagnostics' if path.end_with?('/diagnostics') && path.include?('/runs/')
    return 'executionPlanRequirementRoute' if path.include?('/requirements/{id}/execution-plan')
    return 'mentionPreviewRoute' if path.end_with?('/comments/trigger-preview')
    return 'commentEditRoute' if path == '/api/v1/comments/{id}'
    return 'commentTriggerOutcomesRoute' if path.end_with?('/trigger-outcomes')
    return 'commentRevisionsRoute' if path.end_with?('/revisions')
    return 'commentTriggerRetryRoute' if path.end_with?('/trigger-retry')
    return 'commentFollowUpRoute' if path.end_with?('/follow-up')
    return 'commentRoute' if path.end_with?('/comments')
    return 'pipelineRoute' if path.start_with?('/api/v1/pipelines')
    return 'workflowTemplateRoute' if path.start_with?('/api/v1/workflow-templates')
    return 'chatRoute' if path.start_with?('/api/v1/chats')
    return 'sessionRoute' if path.start_with?('/api/v1/sessions')
    return 'pluginRoute' if path.start_with?('/api/v1/plugins')
    return path == '/api/v1/requirements' ? 'requirements' : 'requirement' if path.start_with?('/api/v1/requirements')
    return path == '/api/v1/bugs' ? 'bugs' : 'bug' if path.start_with?('/api/v1/bugs')
    return 'repositoryRoute' if path.start_with?('/api/v1/repositories')
    return 'repositoryGraph' if path == '/api/v1/repository-graph'
    return 'teamWorkspaceRoute' if path.start_with?('/api/v1/team-workspaces')
    return 'mcpRoute' if path.start_with?('/api/v1/mcp')
    return 'skillRoute' if path.start_with?('/api/v1/skills')
    return 'automationRunRoute' if path.start_with?('/api/v1/automation-runs')
    return 'automationRoute' if path.start_with?('/api/v1/automations')
    return 'runnerRoute' if path.start_with?('/api/v1/runners')
    return 'agentBindingRoute' if path.include?('/agents/{id}/')
    return 'agentRoute' if path == '/api/v1/agents'
    return 'profileRoute' if path.start_with?('/api/v1/developer-profiles')
    return 'approvalRoute' if path.start_with?('/api/v1/approvals')
    return 'evidenceRoute' if path.start_with?('/api/v1/evidence')
    return 'attachmentRoute' if path == '/api/v1/attachments'
    return 'createUpload' if path == '/api/v1/artifacts/uploads'
    return 'createScreenshot' if path == '/api/v1/screenshots'
    return 'uploadRoute' if path.include?('/artifacts/uploads/')
    return 'artifactMigrationRoute' if path.start_with?('/api/v1/artifact-migrations')
    return 'artifactRoute' if path.start_with?('/api/v1/artifacts/')
    return 'listWorkItems' if path == '/api/v1/work-items'
    return 'workItemRoute' if path.start_with?('/api/v1/work-items')
    return 'runRoute' if path.start_with?('/api/v1/runs')
    return 'streamRoute' if path.start_with?('/api/v1/streams/')

    'ServeHTTP'
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
    derived = operations.select { |item| item['operation_id_source'] == 'derived' }
    errors << "all OpenAPI operations must declare operationId (missing: #{derived.length})" unless derived.empty?
    action_ids = actions.map { |item| item['action_id'] }
    errors << 'source action ids must be unique' unless action_ids.uniq.length == action_ids.length
    errors << 'ledger rows must retain the current source SHA' unless rows.all? { |item| item['last_sha'] == source_sha }
    operation_ids.each do |operation_id|
      layers = rows.select { |item| item['operation_id'] == operation_id }.map { |item| item['layer'] }
      errors << "#{operation_id} lacks L0 contract inventory" unless layers.include?('L0-contract-inventory')
      errors << "#{operation_id} lacks L2 API integration" unless layers.include?('L2-api-integration')
    end
    menus.each do |menu|
      action_id = "menu:#{menu}"
      layers = rows.select { |item| item['action_id'] == action_id }.map { |item| item['layer'] }
      errors << "#{action_id} lacks L0 UI inventory" unless layers.include?('L0-ui-inventory')
      errors << "#{action_id} lacks L3 browser coverage" unless layers.include?('L3-browser')
    end
    action_ids.each do |action_id|
      layers = rows.select { |item| item['action_id'] == action_id }.map { |item| item['layer'] }
      errors << "#{action_id} lacks L0 UI inventory" unless layers.include?('L0-ui-inventory')
      errors << "#{action_id} lacks L3 browser coverage" unless layers.include?('L3-browser')
    end
    api_sources = Dir[File.join(root, 'internal', 'api', '*.go')].map { |path| File.read(path) }.join("\n")
    operations.each do |operation|
      handler = operation['handler'].to_s
      errors << "#{operation['operation_id']} maps to missing handler #{handler}" unless api_sources.match?(/func\s+(?:\([^)]*\)\s*)?#{Regexp.escape(handler)}\s*\(/)
    end
    test_contents = {}
    rows.each do |row|
      test_file = row['test_file'].to_s
      test_path = File.join(root, test_file)
      unless File.file?(test_path)
        errors << "#{row['case_id']} references missing test file #{test_file}"
        next
      end
      test_function = row['test_function'].to_s.split('/').first
      contents = test_contents[test_path] ||= File.read(test_path)
      next if test_file.end_with?('.sh') && test_function == 'main'
      next if test_file.end_with?('.go') && contents.match?(/func\s+#{Regexp.escape(test_function)}\s*\(/)
      next if test_file.end_with?('.js') && contents.include?(test_function)
      next if test_file.end_with?('.rb') && contents.match?(/def\s+#{Regexp.escape(test_function)}\b/)

      errors << "#{row['case_id']} references missing test function #{test_function} in #{test_file}"
    end
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
    return @git_sha if defined?(@git_sha)

    stdout, status = Open3.capture2('git', '-C', root, 'rev-parse', 'HEAD')
    @git_sha = status.success? ? stdout.strip : 'unknown'
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
