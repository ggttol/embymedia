# Agent Note: EmbyMedia workspace writes run without approval prompts

Status: implemented

[English](2026-09-01-embymedia-unprompted-workspace-writes.md) | 中文

## Problem

EmbyMedia 部署在业务写入已启用时仍要求每次操作获得明确批准。单一运营者场景没有独立复核者，反复弹窗只会中断日常媒体操作。

## Decision

`deploy/cordis/embymedia.patch.yml` 将组合后的 approval policy 设为 `never`，并把 `workspace-write` 设为默认权限预设且 `approval: never`。新的 EmbyMedia 会话保留 workspace-write 沙箱；包括高风险计划执行在内的操作不再显示 UI 审批卡。持久化计划预览、规范计划哈希、目标重新校验、执行审计、独立验证与 partial 终态保持不变。

## Alternatives considered

**保留逐项审批。** 不采用，因为部署只有一名已明确接受无人值守执行的运营者，重复审批卡会中断常规工作。

**选择 `danger-full-access`。** 不采用，因为关闭审批不需要把文件访问范围从既有 workspace-write 沙箱扩大。

## Consequences

运营者可在没有人工审批暂停的情况下执行破坏性或高成本媒体操作。计划与验证记录成为唯一的站内复核轨迹，因此该部署依赖已认证的单一运营者边界和既有操作保护。