---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# Media Dubbing Agent Example

## User Prompt

`Create me an agent that can transcribe audio and video and redub it with a British accent.`

## What The System Should Infer

The user is asking for a durable media-processing agent, not just a one-off
chat response.

The request decomposes into at least these capabilities:

- media ingestion
- speech-to-text transcription
- optional subtitle generation
- text cleanup or segmentation
- text-to-speech or voice conversion
- audio muxing or video render output

The phrase "transcribe to a British accent" is semantically mixed. The root
assistant should translate that into a clear plan instead of forcing the user
to rephrase perfectly.

## Minimal Clarifying Questions

The root assistant should ask only for missing details such as:

- use local models or external APIs?
- save outputs where?
- transcript only, subtitles too, or full dubbed media?
- generic British voice or cloned voice?
- one-off runs, queued jobs, or watch a folder continuously?

## Draft AgentSpec

Example shape:

- name: `media-dubber`
- purpose: transcribe audio/video and generate British-accent dubbed outputs
- runtime: managed sandbox on this device
- inputs: `mp3`, `wav`, `m4a`, `mp4`, `mov`
- outputs: `.txt`, `.srt`, dubbed audio, dubbed video
- tools: media conversion, transcription, TTS/voice synthesis, file output
- workspace: input and output folders on a selected drive
- secrets: provider API keys if external services are chosen
- approvals: external spend, secret write, writes outside configured output path

## Provisioning Steps

After user approval, the system should:

1. create or select a sandbox/runtime
2. install required media tooling
3. store provider secrets if needed
4. create workspace folders
5. register the new agent in `sky10`
6. offer a test run on a sample file

## Resulting UX

The user should end up with:

- a named durable agent
- a page showing its contract and runtime
- a queue of jobs
- visible artifacts for transcript, subtitles, and dubbed outputs
- logs and retry controls

The key product feeling should be:

- sentence -> spec -> approval -> provisioned agent -> completed artifacts

not:

- sentence -> generic chat answer -> manual setup work
