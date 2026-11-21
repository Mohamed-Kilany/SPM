import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import TestRunRunner from './TestRunRunner.vue'
import { ApiError, api } from '../../api/client'
import type { TestRun } from '../../types/workspace'

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    api: {
      patchTestStepResult: vi.fn(),
      finishTestRun: vi.fn(),
      uploadTestStepEvidence: vi.fn(),
    },
  }
})

const patchStep = vi.mocked(api.patchTestStepResult)
const finishRun = vi.mocked(api.finishTestRun)

function fixtureRun(): TestRun {
  return {
    id: 'run-1',
    project_id: 'project-1',
    name: 'Checkout regression',
    plan_name: 'Release plan',
    suite_name: 'Smoke',
    status: 'planned',
    updated_at: '2026-01-01T00:00:00Z',
    results: [
      {
        id: 'result-1',
        test_case_id: 'case-1',
        case_revision: 2,
        status: 'not_run',
        actual_result: '',
        step_results: [
          {
            id: 'step-1',
            test_result_id: 'result-1',
            case_revision: 2,
            position: 1,
            action: 'Open checkout',
            expected_result: 'Payment action is available',
            test_data: '',
            status: 'not_run',
            actual_result: '',
            evidence_url: '',
            bug_work_item_id: null,
          },
          {
            id: 'step-2',
            test_result_id: 'result-1',
            case_revision: 2,
            position: 2,
            action: 'Confirm payment',
            expected_result: 'Receipt is shown',
            test_data: 'card 4242',
            status: 'not_run',
            actual_result: '',
            evidence_url: '',
            bug_work_item_id: null,
          },
        ],
      },
    ],
  } as unknown as TestRun
}

function mountRunner(run = fixtureRun()) {
  return mount(TestRunRunner, {
    props: {
      open: true,
      run,
      projectId: 'project-1',
      caseTitle: (caseId: string) => (caseId === 'case-1' ? 'Checkout' : caseId),
      canTest: true,
    },
    global: {
      plugins: [createPinia()],
      stubs: { teleport: true },
    },
  })
}

describe('TestRunRunner', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    patchStep.mockReset()
    finishRun.mockReset()
    patchStep.mockResolvedValue({ id: 'step-1', status: 'passed' })
    window.localStorage.clear()
  })

  it('records a step from the P shortcut without a mouse', async () => {
    const wrapper = mountRunner()
    await flushPromises()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'p', bubbles: true }))
    await flushPromises()

    expect(patchStep).toHaveBeenCalledWith(
      'project-1',
      'run-1',
      'result-1',
      'step-1',
      expect.objectContaining({ status: 'passed' }),
    )
    expect(wrapper.text()).toContain('1 passed')
    wrapper.unmount()
  })

  it('autosaves notes after the debounce', async () => {
    const wrapper = mountRunner()
    await flushPromises()

    await wrapper.get('[data-testid="runner-notes"]').setValue('Payment succeeded in 1.2s')
    expect(patchStep).not.toHaveBeenCalled()

    await vi.waitFor(
      () =>
        expect(patchStep).toHaveBeenCalledWith(
          'project-1',
          'run-1',
          'result-1',
          'step-1',
          expect.objectContaining({ actual_result: 'Payment succeeded in 1.2s' }),
        ),
      { timeout: 2500 },
    )
    wrapper.unmount()
  })

  it('requires confirmation before finishing with unexecuted steps', async () => {
    finishRun.mockRejectedValue(new ApiError('2 cases are still not run', 409, 'RUN_INCOMPLETE'))
    const wrapper = mountRunner()
    await flushPromises()

    await wrapper.get('[data-testid="runner-finish"]').trigger('click')
    await flushPromises()

    expect(finishRun).toHaveBeenCalledWith('project-1', 'run-1', false)
    expect(wrapper.text()).toContain('Finish with unexecuted cases?')
    wrapper.unmount()
  })

  it('shows the finish summary and clears the resume key', async () => {
    finishRun.mockResolvedValue({
      id: 'run-1',
      status: 'completed',
      outcome: 'incomplete',
      total: 2,
      passed: 1,
      failed: 0,
      blocked: 0,
      skipped: 0,
      not_run: 1,
      completed_at: '2026-01-01T00:10:00Z',
      already_completed: false,
    })
    const wrapper = mountRunner()
    await flushPromises()

    window.localStorage.setItem('spm.runner.project-1.run-1', '{"notes":{}}')
    await wrapper.get('[data-testid="runner-finish"]').trigger('click')
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('[data-testid="runner-summary"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('incomplete')
    expect(window.localStorage.getItem('spm.runner.project-1.run-1')).toBeNull()
    wrapper.unmount()
  })

  it('resumes at the step saved by the previous session', async () => {
    const first = mountRunner()
    await flushPromises()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    await flushPromises()
    first.unmount()

    const second = mountRunner()
    await flushPromises()

    expect(second.get('[data-testid="runner-step"]').text()).toContain('Confirm payment')
    second.unmount()
  })
})
