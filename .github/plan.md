# Implementation Plan: Native Memory Architecture (The "Cognitive Engine")

## 1. Objective
Transition the Engine AI from a **Retrieval-Augmented Generation (RAG)** model to a **Native Memory Architecture**. The goal is to create a system where project trajectory, architectural decisions, and *cognitive experiences* are implicit "residuals" that shape the AI's reasoning. 

This architecture moves beyond knowing **what** the project is (State) to understanding **how** to work on it (Experience), enabling the AI to evolve and avoid repeated mistakes across all chats.

## 2. The Dual-Track Memory Model
To achieve true evolution, Engine will implement two parallel tracks of memory:

### Track A: The Structural Track (Project State Graph - PSG)
**The "Map" (What is the project?)**
- **Focus**: Technical truth, constraints, and goals.
- **Contents**: Architectural decisions, current milestones, dependency graphs, and "current state of the union."
- **Purpose**: Ensures the AI doesn't suggest something that contradicts the architecture (e.g., suggesting a Monaco-based fix when using a first-party core).

### Track B: The Experiential Track (Attention Residuals)
**The "Intuition" (How do I solve this effectively?)**
- **Focus**: Cognitive patterns, reasoning failures, and "aha!" moments.
- **Contents**: "Lessons Learned" ledger (e.g., *"When modifying the Go sidecar, always check the Tauri event bridge first because that is where the race condition usually hides"*).
- **Purpose**: Prevents the AI from repeating the same reasoning errors. It allows the AI to "evolve" by remembering not just the fix, but the *path* to the fix.

---

## 3. Implementation Roadmap

### Phase 1: The Experience Ledger & PSG Definition
Establish the storage schemas for both tracks.
- **PSG Schema**: Graph nodes for `Decision`, `Goal`, and `Constraint`.
- **Residuals Schema**: A "Cognitive Ledger" consisting of:
    - `Trigger`: The scenario that activates this residual (e.g., "Modifying IPC").
    - `The Pitfall`: What was tried and failed.
    - `The Insight`: The reasoning that led to the solution.
    - `The Heuristic`: The new rule of thumb for future work.

### Phase 2: The Reflection Loop (The "Evolution" Mechanism)
Implementing the process that turns a chat into a residual.
- **Post-Task Distillation**: After a complex bug is fixed or a major feature is implemented, the AI triggers a "Reflection" step.
- **Synthesis**: Instead of just summarizing *what* happened, the AI asks: *"What did I struggle with here, and how can I avoid that struggle in the next chat?"*
- **Commit to Memory**: The resulting insight is written to the Experience Ledger.

### Phase 3: Intuition Injection (Dynamic Context Shaping)
Moving from manual `search_history` to automated "Intuition."
- **Cognitive Triggering**: The system analyzes the current prompt to identify the *type* of work being done (e.g., "Debugging IPC").
- **Residual Injection**: Relevant "Experience" residuals are injected into the system prompt as "Intuitions" (e.g., *"Note: In previous attempts at this, we found that X was a red herring; focus on Y"*).
- **PSG Weighting**: The PSG provides the boundaries (The Map), while the Residuals provide the strategy (The Intuition).

### Phase 4: Validation & "Evolution" Testing
- **The "Amnesia" Test**: Can the AI recall a cognitive shortcut discovered 10 sessions ago?
- **The "Anti-Regression" Test**: If the AI previously struggled with a specific bug and documented the "aha!" moment, does it now solve a similar bug instantly without the struggle?
- **The Trajectory Test**: Can the AI predict the next logical step based on both the PSG (goals) and Residuals (previous efficiencies)?

---

## 4. Technical Requirements

### Backend (Go Sidecar/Storage)
- **Graph Store**: For the PSG.
- **Vector/Pattern Store**: For the Experience Ledger, allowing retrieval based on "cognitive similarity" rather than just keyword matching.

### AI Orchestrator (The "Consciousness" Layer)
- **Reflection Agent**: A specialized prompt/process designed to distill experience into residuals.
- **Context Assembler**: Modified to inject both the "State of the Union" (PSG) and the "Intuitions" (Residuals) into the active window.

## 5. Success Criteria
- **Cognitive Acceleration**: A measurable decrease in the number of turns required to solve similar classes of problems over time.
- **Zero-Prompt Evolution**: The AI behaves as if it has "learned" from previous sessions without the user needing to remind it of past mistakes.
- **Structural Integrity**: High consistency in architectural adherence via the PSG.
