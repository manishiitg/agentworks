import type { AuthUser } from '../services/api'

export function hasWorkflowWriteAccess(user: AuthUser | null | undefined, isMultiUserMode: boolean): boolean {
  if (user?.can_write_workflows !== undefined) {
    return user.can_write_workflows
  }
  if (!isMultiUserMode) {
    return true
  }
  return user?.workflow_access === 'write' || user?.workflow_access === 'owner'
}

export function hasWorkflowOwnerAccess(user: AuthUser | null | undefined, isMultiUserMode: boolean): boolean {
  if (user?.can_manage_workflow_access !== undefined) {
    return user.can_manage_workflow_access
  }
  if (!isMultiUserMode) {
    return true
  }
  return user?.workflow_access === 'owner'
}

// Creating an automation is an account-level permission. A contributor can
// edit an automation assigned to them while still being unable to create a
// new one, so workflow write access must not be used as a substitute here.
export function hasWorkflowCreateAccess(user: AuthUser | null | undefined, isMultiUserMode: boolean): boolean {
  return hasProductCreateAccess(user, isMultiUserMode, 'agentworks')
}

// Creating is allowed per product (PLAT-767): 'agentworks' (workflows), 'relays', 'work' (Crews), 'code',
// 'video-studio'. The server sends the answer per product; older servers only send can_create.
export function hasProductCreateAccess(user: AuthUser | null | undefined, isMultiUserMode: boolean, product: string): boolean {
  if (!isMultiUserMode) {
    return true
  }
  if (user?.is_admin) {
    return true
  }
  const perProduct = user?.can_create_in?.[product]
  if (perProduct !== undefined) {
    return perProduct
  }
  return accountCreateAccess(user)
}

function accountCreateAccess(user: AuthUser | null | undefined): boolean {
  if (!user) {
    return false
  }
  if (user.can_create !== undefined) {
    return user.can_create
  }
  // Compatibility with older servers that did not return can_create.
  return user.workflow_access === 'owner'
}

// True only once the backend has actually confirmed non-write access (PLAT-262
// read tier) — never a guess from the absence of a signal, which is why this
// is `!hasWorkflowWriteAccess` rather than checking `workflow_access === 'read'`
// directly (that field can be absent even when can_write_workflows is set).
export function isWorkflowReadOnly(user: AuthUser | null | undefined, isMultiUserMode: boolean): boolean {
  return !hasWorkflowWriteAccess(user, isMultiUserMode)
}

// Effective access on one workflow, from the /api/auth/me per-workflow map.
// Prefer this over the global flags whenever a workflow id is at hand: a
// global editor can be a viewer (or an owner) on any given workflow.
export function workflowAccessFor(
  user: AuthUser | null | undefined,
  workflowId: string | null | undefined,
): 'owner' | 'write' | 'read' | undefined {
  if (!workflowId) return undefined
  return user?.workflows?.[workflowId]
}
