<script setup lang="ts">
// AI agent skills (capabilities 20 and 21, spec/features/ai-agent-skills).
// Both skills with the AI agents they are installed for, and the consent
// step (REQ:explicit-consent-to-install): the skill's purpose, each AI agent
// the server found with its exact directory, agents not found as such, and
// Install skill / Not now with nothing focused on Install. Nothing is
// written until Install skill is pressed, and the request names only
// harnesses, never a directory (REQ:install-targets-restricted).
// `/skills?skill=todo-demo&from=/demo` opens the consent step directly;
// Not now returns to `from`.
import { computed, nextTick, onMounted, ref } from 'vue'

import { api, type ApiError, type Skill, type SkillInstallDocument, type SkillTarget, type SkillsDocument } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvCommand from '../components/OvCommand.vue'
import OvNotice from '../components/OvNotice.vue'
import OvText from '../components/OvText.vue'
import { t } from '../copy'
import { navigate } from '../router'

const document_ = ref<SkillsDocument | null>(null)
const loadProblem = ref<ApiError | null>(null)
const offered = ref<Skill | null>(null)
const chosen = ref<string[]>([])
const installing = ref(false)
const problem = ref<ApiError | null>(null)
const result = ref<SkillInstallDocument | null>(null)
const heading = ref<HTMLElement>()
const outcome = ref<HTMLElement>()

const query = typeof window === 'undefined' ? new URLSearchParams() : new URLSearchParams(window.location.search)
// Only a console path is followed back.
const from = /^\/[a-z]/.test(query.get('from') ?? '') ? query.get('from')! : null

async function load() {
  const response = await api<SkillsDocument>('GET', '/api/local/v1/skills')
  if (!response.ok) {
    loadProblem.value = response.error
    return
  }
  document_.value = response.data
}

onMounted(async () => {
  await load()
  const id = query.get('skill')
  const skill = document_.value?.skills.find((s) => s.id === id)
  if (skill) offer(skill)
})

function selectable(target: SkillTarget): boolean {
  return target.detected && !!target.harness && target.state !== 'not_ovdb'
}

function offer(skill: Skill) {
  offered.value = skill
  result.value = null
  problem.value = null
  // A copy the person changed since install is replaced only when they tick it.
  chosen.value = skill.targets.filter((target) => selectable(target) && target.state !== 'changed').map((target) => target.harness!)
  void nextTick(() => heading.value?.focus())
}

async function notNow() {
  if (from) {
    navigate(from)
    return
  }
  offered.value = null
  await nextTick()
  window.document.querySelector<HTMLElement>('main h1')?.focus()
}

async function install() {
  if (!offered.value || chosen.value.length === 0) return
  problem.value = null
  installing.value = true
  const response = await api<SkillInstallDocument>('POST', '/api/local/v1/skills/install', {
    skill: offered.value.id,
    harnesses: chosen.value,
    replace_changed: offered.value.targets.some((target) => target.state === 'changed' && chosen.value.includes(target.harness!)),
  })
  installing.value = false
  if (response.ok) {
    result.value = response.data
    offered.value = null
    await load()
  } else {
    problem.value = response.error
  }
  await nextTick()
  outcome.value?.focus()
}

const commands = computed(() => result.value?.next.filter((item) => item.command) ?? [])
const hints = computed(() => result.value?.next.filter((item) => !item.command && !item.action) ?? [])

function installedFor(skill: Skill): string {
  const names = skill.targets
    .filter((target) => target.installed)
    .map((target) => (target.state === 'installed' ? target.name : `${target.name} (${t(`skills.state.${target.state}`)})`))
  return names.length ? t('skills.list.installed_for', { agents: names.join(', ') }) : t('skills.list.not_installed')
}

function go(event: MouseEvent, path: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(path)
}

const link = 'inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2'
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />

    <OvNotice v-if="loadProblem" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />
    <p v-else-if="!document_" class="text-muted">{{ t('console.loading') }}</p>

    <!-- The consent step -->
    <section v-else-if="offered" data-testid="skill-consent" class="flex flex-col gap-6" aria-labelledby="consent-title">
      <h1 id="consent-title" ref="heading" tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">
        {{ t('skills.consent.question', { name: offered.name }) }}
      </h1>
      <OvCard>
        <div class="flex flex-col gap-5">
          <div class="flex flex-col gap-1">
            <p class="text-lg">{{ offered.purpose }}</p>
            <p class="text-muted">{{ offered.example }}</p>
          </div>
          <fieldset class="flex flex-col gap-3">
            <legend class="mb-2 font-semibold">{{ t('skills.consent.install_for') }}</legend>
            <template v-for="target in offered.targets" :key="target.harness ?? target.dir">
              <label v-if="selectable(target)" class="flex cursor-pointer items-start gap-3" :data-harness="target.harness">
                <input
                  v-model="chosen"
                  type="checkbox"
                  :value="target.harness"
                  class="mt-1 size-5 shrink-0 accent-accent"
                />
                <span class="flex min-w-0 flex-col">
                  <span class="font-medium">{{ target.name }}</span>
                  <code class="font-mono text-sm [overflow-wrap:anywhere] text-muted">{{ target.dir }}</code>
                  <span v-if="target.state === 'changed'" class="text-sm text-muted">{{ t('skills.consent.changed') }}</span>
                  <span v-else-if="target.state === 'update_available'" class="text-sm text-muted">{{ t('skills.consent.update_available') }}</span>
                </span>
              </label>
              <p v-else class="flex items-start gap-3 text-muted" :data-harness="target.harness">
                <span aria-hidden="true" class="mt-1 size-5 shrink-0"></span>
                <span>{{ target.name }} — {{ target.state === 'not_ovdb' ? t('skills.state.not_ovdb') : t('skills.state.not_found') }}</span>
              </p>
            </template>
          </fieldset>
          <div class="flex flex-wrap gap-3">
            <OvButton data-testid="install-skill" :busy="installing" @click="install">
              {{ t('skills.consent.install') }}
            </OvButton>
            <OvButton variant="secondary" data-testid="not-now" @click="notNow">{{ t('skills.consent.not_now') }}</OvButton>
          </div>
        </div>
      </OvCard>
      <div ref="outcome" tabindex="-1">
        <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
      </div>
      <div class="flex flex-col gap-2">
        <p class="text-muted">{{ t('browse.same_in_terminal') }}</p>
        <OvCommand :command="offered.command" />
      </div>
    </section>

    <!-- The list, with the last install's Result above it -->
    <template v-else>
      <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('skills.title') }}</h1>
      <div v-if="result" ref="outcome" tabindex="-1" data-testid="skill-result" class="flex flex-col gap-4">
        <OvNotice
          live
          tone="success"
          :title="result.already_up_to_date ? t('skills.up_to_date.title', { name: result.name }) : t('skills.installed.title', { name: result.name })"
        >
          <ul class="flex flex-col gap-1">
            <li v-for="target in result.targets" :key="target.dir" class="[overflow-wrap:anywhere]">
              {{ t('skills.result.line', { name: target.name, path: target.dir }) }}
            </li>
          </ul>
        </OvNotice>
        <section class="flex flex-col gap-3" aria-labelledby="skill-next">
          <h2 id="skill-next" class="text-lg font-semibold tracking-tight">{{ t('home.what_next') }}</h2>
          <p v-for="item in hints" :key="item.label"><OvText :text="item.label" /></p>
          <div class="flex flex-wrap gap-3">
            <template v-for="item in result.next" :key="item.label">
              <a v-if="item.action === 'open_app'" href="/apps/todo/" :class="link">{{ item.label }}</a>
              <a v-else-if="item.action === 'done'" href="/" :class="link" @click="go($event, '/')">{{ item.label }}</a>
            </template>
          </div>
          <div v-if="commands.length" class="flex flex-col gap-2">
            <p class="text-muted">{{ t('browse.same_in_terminal') }}</p>
            <OvCommand v-for="item in commands" :key="item.label" :command="item.command!" />
          </div>
        </section>
      </div>
      <p class="max-w-prose text-lg text-muted">{{ t('skills.intro') }}</p>
      <ul class="flex flex-col gap-4">
        <li v-for="skill in document_.skills" :key="skill.id" :data-skill="skill.id">
          <OvCard>
            <div class="flex flex-col gap-3">
              <h2 class="text-lg font-semibold tracking-tight">{{ skill.name }}</h2>
              <p>{{ skill.purpose }}</p>
              <p class="text-muted" data-testid="installed-for">{{ installedFor(skill) }}</p>
              <div>
                <OvButton variant="secondary" :data-testid="`offer-${skill.id}`" @click="offer(skill)">
                  {{ t('skills.list.install_button', { name: skill.name }) }}
                </OvButton>
              </div>
            </div>
          </OvCard>
        </li>
      </ul>
    </template>
  </div>
</template>
