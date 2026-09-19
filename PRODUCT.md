# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

- **Agents** answer customers on WhatsApp from the shared inbox all day, take conversations from the queue, and log follow-ups.
- **Front-desk and operations staff** run the day-to-day process (reception, clinic or shop managers). They know the business rules cold but not software vocabulary such as "trigger", "webhook" or "payload". They are the people who build automations most of the time (confirmed).
- **Owners and admins** set up the organization, roles, WhatsApp accounts and templates, and review reports.
- Multi-tenant: one installation can serve several organizations, each with its own data, roles and settings.

## Product Purpose

WA CRM is an open-source, self-hosted WhatsApp Business platform shipped as a single binary. It combines a real-time shared inbox, contacts with custom fields, campaigns, chatbot flows, WhatsApp calling with IVR, and CRM modules: follow-ups (tasks), segments, a deal pipeline, automations and reports. Success means that no customer who wrote in is forgotten, and that the follow-up somebody would otherwise have to remember happens on its own.

## Positioning

A WhatsApp-native CRM the organization runs itself: conversations, contact records and the automations acting on them live in one product and one database, rather than a messaging tool bolted onto a separate CRM.

## Operating Context

- Work happens during business hours in the organization's own timezone, and on WhatsApp's terms: a 24-hour service window for free-form replies, Meta-approved templates outside it, and marketing opt-outs.
- The shared inbox is the centre of the day. Conversations move open → pending → resolved and can be snoozed, and transfers put customers in team queues.
- Automations react to CRM changes (a tag, a stage, a task going overdue, a customer going quiet) and do CRM work (tag, assign, create follow-ups, notify, send messages or templates, move deals, call webhooks).
- The interface ships in six languages (en, es, pt, hi, ar, ta), including a right-to-left one (Arabic).

## Capabilities and Constraints

- Role-based permissions per resource and action. Agents often see only their own contacts and conversations.
- The automation engine runs rules made of a trigger, an optional contact filter, and ordered steps. It has loop protection (depth 3), per-rule run policies (once per contact, cooldown, hourly cap), idempotent runs, run history, and a dry-run tester that writes nothing.
- The action library is shared by automations, chatbot CRM-action nodes and keyword rules. What an action needs must stay consistent across all three.
- Sends respect the service window, template approval and marketing consent. A rule can never message someone WhatsApp would not allow.

## Brand Commitments

- Name: **WA CRM**.
- Voice (inferred from the product's copy; confirm): plain words, and say why. Labels name the thing the person does, not the system's term for it. Errors say what happened and what to do next.

## Evidence on Hand

- Screenshots in `docs/public/images/` and product docs under `docs/`.
- No customer names, testimonials, metrics or pricing exist in the repository. Do not invent them.

## Product Principles

1. **Nobody gets forgotten.** Anything waiting on a person is visible, owned, and eventually chased.
2. **Plain language over system vocabulary.** Someone who runs a front desk should be able to read every screen without a glossary.
3. **Safe to try.** Anything that can message customers is previewable and testable before it runs.
4. **Powerful without being dense.** Complex capability is reached by adding one more plain step, not through a separate expert mode.
5. **One source of truth.** The same record, rule or action reads the same everywhere it appears.

## Accessibility & Inclusion

- Six locales including RTL Arabic, so layouts must not assume left-to-right or English string lengths (inferred from the shipped locales).
- Keyboard operation and visible focus are expected throughout the product (inferred from existing shortcut and focus work; no formal WCAG target has been stated).
