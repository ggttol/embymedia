/** `approval` namespace dictionaries. */

/** Simplified Chinese dictionary and key-set source of truth. */
export const zh = {
  waiting: '等待审批 / Waiting for approval',
  'detail.aria': '审批详情 / Approval details',
  escalation: '工具 {toolName} 请求执行受限操作',
  reject: '拒绝',
  allowOnce: '仅允许一次',
} satisfies Record<string, string>

/** Approval dictionary key union. */
export type ApprovalKey = keyof typeof zh

/** English dictionary, checked against the Chinese key set. */
export const en = {
  waiting: 'Waiting for approval / 等待审批',
  'detail.aria': 'Approval details / 审批详情',
  escalation: 'Tool {toolName} requests privileged execution / 请求执行受限操作',
  reject: 'Reject / 拒绝',
  allowOnce: 'Allow once / 仅允许一次',
} satisfies Record<ApprovalKey, string>
