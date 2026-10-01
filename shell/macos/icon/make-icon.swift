// Draws ARFABIT's app icon, a disc in a clear case, as AppIcon.icns. build.sh
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

/// The colour of a disc's surface at an angle around it, in degrees: silver,
/// with the faint rainbow a disc's surface throws back, brightest where the
/// light catches it.
func shine(_ degrees: CGFloat) -> CGColor {
    let a = degrees * .pi / 180

    // Two bright sweeps opposite each other, as on a real disc.
    let light = pow(0.5 + 0.5 * cos(2 * (a - 0.7)), 1.6)

    // Hue goes round twice, so each sweep has its own rainbow.
    let hue = (2 * a / (2 * .pi)).truncatingRemainder(dividingBy: 1)
    let (hr, hg, hb) = rainbow(hue)

    // Darker silver between the sweeps, nearly white in them, and the
    // rainbow strongest at their edges.
    let silver = 0.60 + 0.38 * light
    let tint = 0.08 + 1.5 * light * (1 - light)

    // Cool, slightly blue silver between the sweeps, never brown.
    let cool = 0.05 * (1 - light)
    return CGColor(
        srgbRed: silver * (1 - tint) + hr * tint - cool,
        green: silver * (1 - tint) + hg * tint,
        blue: silver * (1 - tint) + hb * tint + cool,
        alpha: 1)
}

/// A soft rainbow: cyan, violet, pink, gold, green, and round again.
func rainbow(_ t: CGFloat) -> (CGFloat, CGFloat, CGFloat) {
    let stops: [(CGFloat, CGFloat, CGFloat)] = [
        (0.35, 0.80, 1.00), (0.62, 0.45, 1.00), (1.00, 0.45, 0.80),
        (1.00, 0.85, 0.55), (0.50, 0.95, 0.85), (0.35, 0.80, 1.00),
    ]
    let x = t * CGFloat(stops.count - 1)
    let i = min(Int(x), stops.count - 2)
    let f = x - CGFloat(i)
    let (a, b) = (stops[i], stops[i + 1])
    return (a.0 + (b.0 - a.0) * f, a.1 + (b.1 - a.1) * f, a.2 + (b.2 - a.2) * f)
}

/// Draws the icon on a 1024-point canvas, origin at the bottom left: a disc in
/// a clear plastic case with a metal shutter.
func draw(_ c: CGContext) {
    func circle(_ p: CGPoint, _ r: CGFloat) -> CGPath {
        CGPath(ellipseIn: CGRect(x: p.x - r, y: p.y - r, width: 2 * r, height: 2 * r), transform: nil)
    }

    // The case, on Apple's icon grid: 824 points square, inset 100.
    let tile = CGRect(x: 100, y: 100, width: 824, height: 824)
    let casePath = CGPath(roundedRect: tile, cornerWidth: 150, cornerHeight: 150, transform: nil)

    c.saveGState()
    c.setShadow(offset: CGSize(width: 0, height: -10), blur: 24, color: color(0x000000, 0.18))
    c.addPath(casePath)
    c.setFillColor(color(0xF4F6FA, 0.35))
    c.fillPath()
    c.restoreGState()

    // Clear plastic with a white frost: what is behind the icon shows
    // through, a little more at the bottom.
    c.saveGState()
    c.addPath(casePath)
    c.clip()
    c.drawLinearGradient(
        gradient([color(0xFFFFFF, 0.55), color(0xEEF1F5, 0.35), color(0xE2E7EE, 0.30)], [0, 0.6, 1]),
        start: CGPoint(x: 512, y: 924), end: CGPoint(x: 512, y: 100), options: [])
    c.restoreGState()

    // The moulded ridge just inside the edge, and the case's small round marks.
    let ridge = CGPath(roundedRect: tile.insetBy(dx: 26, dy: 26), cornerWidth: 126, cornerHeight: 126, transform: nil)
    c.addPath(ridge)
    c.setLineWidth(5)
    c.setStrokeColor(color(0xAAB3C0, 0.55))
    c.strokePath()
    for p in [CGPoint(x: 190, y: 834), CGPoint(x: 834, y: 834), CGPoint(x: 190, y: 190), CGPoint(x: 834, y: 190)] {
        c.addPath(circle(p, 16))
        c.setLineWidth(4)
        c.setStrokeColor(color(0xAAB3C0, 0.6))
        c.strokePath()
    }

    // The disc.
    let centre = CGPoint(x: 476, y: 528)
    let discR: CGFloat = 300

    c.saveGState()
    c.setShadow(offset: CGSize(width: 0, height: -4), blur: 10, color: color(0x000000, 0.25))
    c.addPath(circle(centre, discR))
    c.setFillColor(color(0xC8CDD5))
    c.fillPath()
    c.restoreGState()

    // Its surface, in thin slices round the centre: CoreGraphics has no fill
    // that changes colour round a point.
    c.saveGState()
    c.addPath(circle(centre, discR))
    c.clip()
    let slices = 720
    for i in 0..<slices {
        let a0 = CGFloat(i) / CGFloat(slices) * 2 * .pi
        let a1 = CGFloat(i + 1) / CGFloat(slices) * 2 * .pi + 0.003
        c.move(to: centre)
        c.addArc(center: centre, radius: discR, startAngle: a0, endAngle: a1, clockwise: false)
        c.closePath()
        c.setFillColor(shine(a0 * 180 / .pi))
        c.fillPath()
    }
    // A soft brightening across the middle, as from a light above.
    c.drawRadialGradient(
        gradient([color(0xFFFFFF, 0.35), color(0xFFFFFF, 0)], [0, 1]),
        startCenter: CGPoint(x: centre.x - 60, y: centre.y + 90), startRadius: 0,
        endCenter: CGPoint(x: centre.x - 60, y: centre.y + 90), endRadius: 260, options: [])
    c.restoreGState()

    c.addPath(circle(centre, discR - 1))
    c.setLineWidth(2)
    c.setStrokeColor(color(0x7E8794, 0.5))
    c.strokePath()

    // The clear ring round the hub, and its printed edge.
    c.addPath(circle(centre, 128))
    c.setFillColor(color(0xE9EDF2, 0.75))
    c.fillPath()
    c.addPath(circle(centre, 128))
    c.setLineWidth(10)
    c.setStrokeColor(color(0x3A4048, 0.55))
    c.strokePath()

    // The metal hub, rings within rings.
    c.saveGState()
    c.addPath(circle(centre, 82))
    c.clip()
    c.drawLinearGradient(
        gradient([color(0xE4E7EB), color(0x9AA1AB), color(0xC9CED5)], [0, 0.5, 1]),
        start: CGPoint(x: centre.x - 82, y: centre.y + 82), end: CGPoint(x: centre.x + 82, y: centre.y - 82), options: [])
    c.restoreGState()
    for (r, alpha) in [(CGFloat(82), CGFloat(0.5)), (60, 0.35), (40, 0.4)] {
        c.addPath(circle(centre, r))
        c.setLineWidth(3)
        c.setStrokeColor(color(0x4C535C, alpha))
        c.strokePath()
    }
    c.addPath(circle(centre, 13))
    c.setFillColor(color(0xFFFFFF))
    c.fillPath()

    // The case's clear window over the disc.
    let window = CGPath(roundedRect: CGRect(x: 214, y: 236, width: 360, height: 584), cornerWidth: 24, cornerHeight: 24, transform: nil)
    c.addPath(window)
    c.setFillColor(color(0xFFFFFF, 0.06))
    c.fillPath()
    c.addPath(window)
    c.setLineWidth(3)
    c.setStrokeColor(color(0xFFFFFF, 0.55))
    c.strokePath()

    // The metal shutter, slid across from the right.
    let shutter = CGRect(x: 588, y: 400, width: 306, height: 280)
    let shutterPath = CGPath(roundedRect: shutter, cornerWidth: 14, cornerHeight: 14, transform: nil)
    c.saveGState()
    c.setShadow(offset: CGSize(width: -4, height: -6), blur: 14, color: color(0x000000, 0.3))
    c.addPath(shutterPath)
    c.setFillColor(color(0xB4BAC2))
    c.fillPath()
    c.restoreGState()
    c.saveGState()
    c.addPath(shutterPath)
    c.clip()
    c.drawLinearGradient(
        gradient([color(0xD9DDE2), color(0xA9B0B9), color(0xC4C9D0)], [0, 0.55, 1]),
        start: CGPoint(x: shutter.minX, y: shutter.maxY), end: CGPoint(x: shutter.maxX, y: shutter.minY), options: [])
    // Brushed lines.
    c.setLineWidth(1)
    for y in stride(from: shutter.minY + 4, to: shutter.maxY, by: 5) {
        c.move(to: CGPoint(x: shutter.minX, y: y))
        c.addLine(to: CGPoint(x: shutter.maxX, y: y))
        c.setStrokeColor(color(0xFFFFFF, (y.truncatingRemainder(dividingBy: 10) < 5) ? 0.10 : 0.04))
        c.strokePath()
    }
    c.restoreGState()
    let inset = CGPath(roundedRect: shutter.insetBy(dx: 18, dy: 18), cornerWidth: 8, cornerHeight: 8, transform: nil)
    c.addPath(inset)
    c.setLineWidth(3)
    c.setStrokeColor(color(0x7D8590, 0.45))
    c.strokePath()

    // The arrow that says which way the disc goes in.
    c.move(to: CGPoint(x: 476, y: 884))
    c.addLine(to: CGPoint(x: 452, y: 842))
    c.addLine(to: CGPoint(x: 500, y: 842))
    c.closePath()
    c.setFillColor(color(0x1E2228))
    c.fillPath()

    // Light along the case's top edge.
    c.saveGState()
    c.addPath(casePath)
    c.setLineWidth(6)
    c.replacePathWithStrokedPath()
    c.clip()
    c.drawLinearGradient(
        gradient([color(0xFFFFFF, 0.9), color(0xFFFFFF, 0.1)], [0, 1]),
        start: CGPoint(x: 512, y: 924), end: CGPoint(x: 512, y: 500), options: [])
    c.restoreGState()
    c.addPath(casePath)
    c.setLineWidth(2)
    c.setStrokeColor(color(0x8C96A3, 0.6))
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
