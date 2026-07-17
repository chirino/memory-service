package memories.attributes

# Optional cognition policy for deployments that want type-aware memory search.
# It preserves the built-in namespace/sub attributes and adds only safe, compact
# cognition metadata. Do not extract raw content, citations, prompts, provider IDs,
# or client metadata.

default attributes = {}

base_attributes = {"namespace": input.namespace[0], "sub": input.namespace[1]} if {
    count(input.namespace) >= 2
}

base_attributes = {} if {
    count(input.namespace) < 2
}

memory_kind = kind if {
    is_string(input.value.kind)
    kind := input.value.kind
}

memory_kind = kind if {
    not is_string(input.value.kind)
    count(input.namespace) > 0
    kind := input.namespace[count(input.namespace) - 1]
}

cognition_attributes["memoryKind"] = kind if {
    kind := memory_kind
}

cognition_attributes["runtimeId"] = runtime_id if {
    is_string(input.value.provenance.runtime_id)
    runtime_id := input.value.provenance.runtime_id
}

cognition_attributes["runtimeId"] = runtime_id if {
    not is_string(input.value.provenance.runtime_id)
    is_string(input.value.runtime.id)
    runtime_id := input.value.runtime.id
}

cognition_attributes["runtimeVersion"] = runtime_version if {
    is_string(input.value.provenance.runtime_version)
    runtime_version := input.value.provenance.runtime_version
}

cognition_attributes["runtimeVersion"] = runtime_version if {
    not is_string(input.value.provenance.runtime_version)
    is_string(input.value.runtime.version)
    runtime_version := input.value.runtime.version
}

cognition_attributes["confidence"] = "high" if {
    confidence := input.value.confidence
    is_number(confidence)
    confidence >= 0.8
}

cognition_attributes["confidence"] = "medium" if {
    confidence := input.value.confidence
    is_number(confidence)
    confidence >= 0.5
    confidence < 0.8
}

cognition_attributes["confidence"] = "low" if {
    confidence := input.value.confidence
    is_number(confidence)
    confidence < 0.5
}

cognition_attributes["conversationIds"] = conversation_id if {
    is_string(input.value.provenance.conversation_id)
    conversation_id := input.value.provenance.conversation_id
}

cognition_attributes["entryIds"] = entry_id if {
    is_array(input.value.provenance.entry_ids)
    count(input.value.provenance.entry_ids) > 0
    entry_id := input.value.provenance.entry_ids[0]
}

attributes = object.union(base_attributes, cognition_attributes)
