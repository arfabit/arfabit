// Draws ARFABIT's app icon, a disc on a dark tile, as AppIcon.icns. build.sh
// runs it:
//
//   swift shell/macos/icon/make-icon.swift AppIcon.icns
//
// Drawn here rather than kept as a picture so it can be changed in a text
// editor. The menu bar uses the system's own disc symbol, which Apple does not
// allow in app icons, so this is a disc of ARFABIT's own.

import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

func color(_ hex: UInt32, _ alpha: CGFloat = 1) -> CGColor {
    CGColor(
        srgbRed: CGFloat((hex >> 16) & 0xff) / 255,
        green: CGFloat((hex >> 8) & 0xff) / 255,
        blue: CGFloat(hex & 0xff) / 255,
        alpha: alpha)
}

func gradient(_ colors: [CGColor], _ stops: [CGFloat]) -> CGGradient {
    CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB), colors: colors as CFArray, locations: stops)!
}

/// Draws the icon on a 1024-point canvas, origin at the bottom left.
func draw(_ c: CGContext) {
    // The tile, on Apple's icon grid: 824 points square, inset 100.
    let tile = CGRect(x: 100, y: 100, width: 824, height: 824)
    let tilePath = CGPath(roundedRect: tile, cornerWidth: 185, cornerHeight: 185, transform: nil)
    let background = gradient([color(0x33415C), color(0x111722)], [0, 1])

    c.saveGState()
    c.setShadow(offset: CGSize(width: 0, height: -10), blur: 24, color: color(0x000000, 0.35))
    c.addPath(tilePath)
    c.setFillColor(color(0x111722))
    c.fillPath()
    c.restoreGState()

    c.saveGState()
    c.addPath(tilePath)
    c.clip()
    c.drawLinearGradient(background, start: CGPoint(x: 512, y: 924), end: CGPoint(x: 512, y: 100), options: [])
    c.restoreGState()

    let centre = CGPoint(x: 512, y: 512)
    func circle(_ r: CGFloat) -> CGPath {
        CGPath(ellipseIn: CGRect(x: centre.x - r, y: centre.y - r, width: 2 * r, height: 2 * r), transform: nil)
    }
    let discR: CGFloat = 330
    let hubR: CGFloat = 100
    let holeR: CGFloat = 40

    // The disc, with a shadow beneath it.
    c.saveGState()
    c.setShadow(offset: CGSize(width: 0, height: -12), blur: 30, color: color(0x000000, 0.45))
    c.addPath(circle(discR))
    c.setFillColor(color(0xB9C1CC))
    c.fillPath()
    c.restoreGState()

    // Its silver face, lit from the top left.
    c.saveGState()
    c.addPath(circle(discR))
    c.clip()
    c.drawLinearGradient(
        gradient([color(0xF3F5F8), color(0xC5CCD6), color(0x9AA4B2)], [0, 0.55, 1]),
        start: CGPoint(x: 280, y: 760), end: CGPoint(x: 744, y: 264), options: [])

    // Light catching the surface: two opposite wedges, faintly coloured at
    // their edges as a real disc is.
    func wedge(_ from: CGFloat, _ to: CGFloat, _ fill: CGColor) {
        c.move(to: centre)
        c.addArc(center: centre, radius: discR, startAngle: from * .pi / 180, endAngle: to * .pi / 180, clockwise: false)
        c.closePath()
        c.setFillColor(fill)
        c.fillPath()
    }
    for base in [CGFloat(35), 215] {
        wedge(base - 14, base - 6, color(0x6FD3FF, 0.22))
        wedge(base - 6, base + 10, color(0xFFFFFF, 0.55))
        wedge(base + 10, base + 18, color(0xFF8AD8, 0.18))
    }

    // Faint rings where the data is.
    c.setLineWidth(1.5)
    for r in stride(from: CGFloat(150), through: 320, by: 14) {
        c.addPath(circle(r))
        c.setStrokeColor(color(0x000000, 0.035))
        c.strokePath()
    }
    c.restoreGState()

    // The rim.
    c.addPath(circle(discR - 1.5))
    c.setLineWidth(3)
    c.setStrokeColor(color(0x5F6977, 0.6))
    c.strokePath()

    // The clear hub, and its edge.
    c.addPath(circle(hubR))
    c.setFillColor(color(0xD9DEE5))
    c.fillPath()
    c.addPath(circle(hubR))
    c.setLineWidth(4)
    c.setStrokeColor(color(0x7D8795, 0.55))
    c.strokePath()

    // The hole, through to the tile behind.
    c.saveGState()
    c.addPath(circle(holeR))
    c.clip()
    c.drawLinearGradient(background, start: CGPoint(x: 512, y: 924), end: CGPoint(x: 512, y: 100), options: [])
    c.restoreGState()
    c.addPath(circle(holeR))
    c.setLineWidth(4)
    c.setStrokeColor(color(0x5F6977, 0.7))
    c.strokePath()
}

func png(size: Int) -> Data {
    let c = CGContext(
        data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: 0,
        space: CGColorSpace(name: CGColorSpace.sRGB)!,
        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    c.interpolationQuality = .high
    c.scaleBy(x: CGFloat(size) / 1024, y: CGFloat(size) / 1024)
    draw(c)

    let data = NSMutableData()
    let dest = CGImageDestinationCreateWithData(data as CFMutableData, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(dest, c.makeImage()!, nil)
    guard CGImageDestinationFinalize(dest) else { fatalError("could not make a \(size) pixel picture") }
    return data as Data
}

guard CommandLine.arguments.count == 2 else {
    FileHandle.standardError.write("usage: make-icon.swift AppIcon.icns\n".data(using: .utf8)!)
    exit(2)
}

// An .icns file is a list of pictures, each tagged with the size it is for:
// a four-letter type, its length, and a PNG. These are every size Finder, the
// Dock and Spotlight ask for, on ordinary and Retina screens.
let entries: [(String, Int)] = [
    ("icp4", 16), ("ic11", 32),     // 16 points, and on Retina
    ("icp5", 32), ("ic12", 64),     // 32
    ("ic07", 128), ("ic13", 256),   // 128
    ("ic08", 256), ("ic14", 512),   // 256
    ("ic09", 512), ("ic10", 1024),  // 512
]

func bigEndian(_ n: Int) -> Data {
    withUnsafeBytes(of: UInt32(n).bigEndian) { Data($0) }
}

var body = Data()
for (type, size) in entries {
    let picture = png(size: size)
    body += Data(type.utf8) + bigEndian(8 + picture.count) + picture
}
let file = Data("icns".utf8) + bigEndian(8 + body.count) + body
try file.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
