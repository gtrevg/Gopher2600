#version 150

// majority of ideas taken from Mattias Gustavsson's crt-view. much of the
// implementation details are also from here.
//
//		https://github.com/mattiasgustavsson/crtview/
//
// other ideas taken from the crt-pi.glsl shader which is part of lib-retro:
//
//		https://github.com/libretro/glsl-shaders/blob/master/crt/shaders/crt-pi.glsl

uniform sampler2D Texture;
uniform sampler2D Frame;
in vec2 Frag_UV;
in vec4 Frag_Color;
out vec4 Out_Color;

uniform vec2 ScreenDim;
uniform int NumScanlines;
uniform int NumClocks;
uniform int Curve;
uniform int ShadowMask;
uniform int Scanlines;
uniform int Noise;
uniform int Fringing;
uniform float CurveAmount;
uniform float MaskBright;
uniform float ScanlinesBright;
uniform float NoiseLevel;
uniform float FringingAmount;
uniform float Time;


// Gold Noise taken from: https://www.shadertoy.com/view/ltB3zD
// Coprighted to dcerisano@standard3d.com not sure of the licence

// Gold Noise ©2015 dcerisano@standard3d.com
// - based on the Golden Ratio
// - uniform normalized distribution
// - fastest static noise generator function (also runs at low precision)
float PHI = 1.61803398874989484820459;  // Φ = Golden Ratio   
float gold_noise(in vec2 xy){
	return fract(tan(distance(xy*PHI, xy)*Time)*xy.x);
}

// taken directly from https://github.com/mattiasgustavsson/crtview/
vec2 curve(in vec2 uv)
{
	uv = (uv - 0.5) * 2.1;
	uv *= 1.1;	
	uv.x *= 1.0 + pow((abs(uv.y) / 5.0), 2.0);
	uv.y *= 1.0 + pow((abs(uv.x) / 4.0), 2.0);
	uv  = (uv / 2.0) + 0.5;
	uv =  uv * 0.92 + 0.04;
	return uv;
}

void main() {
	vec4 Crt_Color;
	vec2 uv = Frag_UV;

	if (Curve == 1) {
		// curve UV coordinates. 
		float m = (CurveAmount * 0.4) + 0.6; // bring into sensible range
		uv = mix(curve(uv), uv, m);
	}

	// after this point every UV reference is to the curved UV

	// basic color
	Crt_Color = Frag_Color * texture(Texture, uv.st);

	// scanlines -  only draw if texture is big enough
	if (Scanlines == 1 &&float(ScreenDim.y)/float(NumScanlines) > 2.0) {
		float scans = clamp(ScanlinesBright+0.18*sin(uv.y*ScreenDim.y*1.5), 0.0, 1.0);
		float s = pow(scans,0.9);
		Crt_Color.rgb *= vec3(s);
	}

	// shadow mask - only draw if texture is big enough
	if (ShadowMask == 1 && float(ScreenDim.x)/float(NumClocks) > 3.0) {
		if (mod(floor(gl_FragCoord.x), 2) == 0.0) {
			Crt_Color.rgb *= MaskBright;
		}
	}

	// noise (includes flicker)
	if (Noise == 1) {
		float n;
		n = gold_noise(gl_FragCoord.xy);
		if (n < 0.33) {
			Crt_Color.r *= max(1.0-NoiseLevel, n);
		} else if (n < 0.66) {
			Crt_Color.g *= max(1.0-NoiseLevel, n);
		} else {
			Crt_Color.b *= max(1.0-NoiseLevel, n);
		}

		// flicker
		/* float level = 0.004; */
		/* Crt_Color *= (1.0-level*(sin(50.0*Time+uv.y*2.0)*0.5+0.5)); */
	}

	// fringing (chromatic aberration)
	vec2 ab = vec2(0.0);
	if (Fringing == 1) {
		if (Curve == 1) {
			ab.x = abs(uv.x-0.5);
			ab.y = abs(uv.y-0.5);

			// modulate fringing amount by curvature
			float m = 0.020 - (0.010 * CurveAmount);
			float l = FringingAmount * m;

			// aberration amount limited to reasonable values
			ab *= l;

			// minimum amount of aberration
			ab = clamp(vec2(0.04), ab, ab);
		} else {
			ab.x = abs(uv.x-0.5);
			ab.y = abs(uv.y-0.5);
			ab *= FringingAmount * 0.015;
		}
	}

	// adjust sign depending on which quadrant the pixel is in
	if (uv.x <= 0.5) {
		ab.x *= -1;
	}
	if (uv.y <= 0.5) {
		ab.y *= -1;
	}

	// perform the aberration
	Crt_Color.r += texture(Texture, vec2(uv.x+(1.0*ab.x), uv.y+(1.0*ab.y))).r;
	Crt_Color.g += texture(Texture, vec2(uv.x+(1.4*ab.x), uv.y+(1.4*ab.y))).g;
	Crt_Color.b += texture(Texture, vec2(uv.x+(1.8*ab.x), uv.y+(1.8*ab.y))).b;
	Crt_Color.rgb *= 0.50;

	// vignette effect
	if (Curve == 1) {
		float vignette = 10*uv.x*uv.y*(1.0-uv.x)*(1.0-uv.y);
		Crt_Color.rgb *= pow(vignette, 0.10) * 1.3;
	}

	// finalise color
	Out_Color = Crt_Color;
}