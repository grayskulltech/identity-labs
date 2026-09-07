import Foundation
import FoundationModels

/// Converts an MCP tool's JSON Schema `inputSchema` into a Foundation Models
/// `GenerationSchema` at runtime, so the model produces arguments the gateway accepts.
///
/// Supported: object, string, string enum, integer, number, boolean, array, nested
/// objects, `required`. Unsupported constructs fall back to a described string so the
/// tool remains callable; the description carries the intent to the model.
public enum SchemaBridge {
    public static func generationSchema(for tool: MCPToolDefinition) throws -> GenerationSchema {
        let root = dynamicSchema(
            from: tool.inputSchema,
            name: sanitizedName(tool.name),
            description: tool.description
        )
        return try GenerationSchema(root: root, dependencies: [])
    }

    static func dynamicSchema(from schema: JSONValue, name: String, description: String?) -> DynamicGenerationSchema {
        let resolvedDescription = description ?? schema["description"]?.stringValue

        if let choices = schema["enum"]?.arrayValue?.compactMap(\.stringValue), !choices.isEmpty {
            return DynamicGenerationSchema(name: name, description: resolvedDescription, anyOf: choices)
        }

        let type = schema["type"]?.stringValue ?? (schema["properties"] != nil ? "object" : "string")

        switch type {
        case "object":
            let properties = schema["properties"]?.objectValue ?? [:]
            let required = Set(schema["required"]?.arrayValue?.compactMap(\.stringValue) ?? [])
            let members = properties.keys.sorted().map { key -> DynamicGenerationSchema.Property in
                let propertySchema = properties[key] ?? .object([:])
                return DynamicGenerationSchema.Property(
                    name: key,
                    description: propertySchema["description"]?.stringValue,
                    schema: dynamicSchema(from: propertySchema, name: "\(name)_\(key)", description: nil),
                    isOptional: !required.contains(key)
                )
            }
            return DynamicGenerationSchema(name: name, description: resolvedDescription, properties: members)

        case "integer":
            return DynamicGenerationSchema(type: Int.self, guides: [])

        case "number":
            return DynamicGenerationSchema(type: Double.self, guides: [])

        case "boolean":
            return DynamicGenerationSchema(type: Bool.self, guides: [])

        case "array":
            let items = schema["items"] ?? .object(["type": .string("string")])
            return DynamicGenerationSchema(
                arrayOf: dynamicSchema(from: items, name: "\(name)_item", description: nil),
                minimumElements: schema["minItems"]?.intValue,
                maximumElements: schema["maxItems"]?.intValue
            )

        default:
            return DynamicGenerationSchema(type: String.self, guides: [])
        }
    }

    /// Schema names must be identifier-like for the framework; MCP tool names may carry
    /// dashes or dots.
    static func sanitizedName(_ name: String) -> String {
        let cleaned = name.map { $0.isLetter || $0.isNumber ? $0 : "_" }
        let joined = String(cleaned)
        return joined.first?.isNumber == true ? "_" + joined : joined
    }
}
